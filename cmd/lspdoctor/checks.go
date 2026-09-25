package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/broker"
)

// status is the outcome of one doctor check, mirroring mcp-gql-kit's
// cmd/mgk/doctor.go checkStatus shape (the in-house convention this tool
// follows) rather than inventing a new one.
type status int

const (
	statusPass status = iota
	statusWarn
	statusFail
	statusSkip
	statusInfo
)

func (s status) String() string {
	switch s {
	case statusPass:
		return "PASS"
	case statusWarn:
		return "WARN"
	case statusFail:
		return "FAIL"
	case statusSkip:
		return "SKIP"
	case statusInfo:
		return "INFO"
	default:
		return "?"
	}
}

// result is one line of doctor output.
type result struct {
	name   string
	status status
	detail string
}

func (r result) String() string {
	if r.detail == "" {
		return fmt.Sprintf("[%s] %s", r.status, r.name)
	}
	return fmt.Sprintf("[%s] %s: %s", r.status, r.name, r.detail)
}

// checkBinary reports whether the lspbroker binary resolves on PATH.
func checkBinary() result {
	const name = "lspbroker binary on PATH"
	path, err := exec.LookPath("lspbroker")
	if err != nil {
		return result{name: name, status: statusFail,
			detail: "not found (" + err.Error() + ") — build/install with: make install-service"}
	}
	return result{name: name, status: statusPass, detail: path}
}

// checkService reports the systemd --user state of lspbridge.service. It
// never fails the check merely because the unit is not installed or not
// running — plenty of valid setups run the daemon another way (a login-shell
// background process, a different supervisor) — it only reports what it
// found. SKIP, not FAIL/WARN, when systemctl itself is unavailable (e.g. no
// systemd on this host).
func checkService() result {
	const name = "systemd --user service lspbridge.service"
	if _, err := exec.LookPath("systemctl"); err != nil {
		return result{name: name, status: statusSkip, detail: "systemctl not on PATH"}
	}
	out, _ := exec.Command("systemctl", "--user", "is-active", "lspbridge.service").Output()
	state := strings.TrimSpace(string(out))
	if state == "" {
		state = "unknown"
	}
	if state == "active" {
		return result{name: name, status: statusPass, detail: state}
	}
	return result{name: name, status: statusWarn,
		detail: state + " — not necessarily a problem if the daemon is started another way, or not yet installed"}
}

// consumerSocketPath returns the socket path [broker.DefaultSocketPath]
// resolves to in the real consumers' spawn environment: mcp-ast and
// mcp-agent-editor, both spawned by the mgk MCP gateway.
//
// RE-MEASURED 2026-09-25 (supersedes the 2026-08-12 behavior described
// below, kept as history): reading /proc/<pid>/environ live for both
// consumers now shows XDG_RUNTIME_DIR=/run/user/<uid> present in BOTH —
// mgk's spawn environment changed since the original measurement, which
// found neither var present at all. TMPDIR is still absent from both
// consumers' environments today, but that no longer changes the answer:
// DefaultSocketPath checks XDG_RUNTIME_DIR FIRST and returns from that
// branch whenever it is non-empty, so TMPDIR is irrelevant once
// XDG_RUNTIME_DIR is set.
//
// This function does NOT read XDG_RUNTIME_DIR from ITS OWN (the doctor
// process's) ambient environment — doing so would repeat, one variable up,
// the exact mistake this file's history already records for TMPDIR: an
// operator running `make doctor` from a session where the login manager
// never set XDG_RUNTIME_DIR (a bare SSH session with no pam_systemd, say)
// would make doctor's notion of "the consumer path" depend on the
// OPERATOR's incidental environment rather than the consumer's real one.
// Instead it forces XDG_RUNTIME_DIR to the uid-derived value
// "/run/user/<uid>" directly — not a guess: this is exactly what
// pam_systemd and the systemd --user manager both set for every login
// session and every unit they spawn (systemd.unit(5)'s `%t` specifier:
// "for user managers ... the path $XDG_RUNTIME_DIR resolves to"), and the
// 2026-09-25 measurement confirms mgk now propagates that same value,
// unchanged, to the processes it spawns.
//
// SUPERSEDED 2026-08-12 behavior (history, not current): at that time the
// real consumers' spawn environment carried NEITHER XDG_RUNTIME_DIR NOR
// TMPDIR (verified via /proc/<mcp-ast-pid>/environ — five vars total,
// neither among them), so this function UNSET both vars to model that
// filtered environment before delegating to broker.DefaultSocketPath().
// That environment no longer exists — see above — and reproducing it here
// today would make doctor disagree with the real consumers rather than
// agree with them, which is exactly the divergence this tool exists to
// catch, not manufacture.
//
// This delegates to the same function the broker and its consumers actually
// call rather than re-deriving the fallback formula by hand, so doctor's
// notion of "the consumer path" and the broker's own notion can never drift
// apart. The env mutation is process-local and restored before return, so
// it is invisible to anything else running in this process or its parent
// shell.
func consumerSocketPath() string {
	restore := setForDuration("XDG_RUNTIME_DIR", fmt.Sprintf("/run/user/%d", os.Getuid()))
	defer restore()
	return broker.DefaultSocketPath()
}

// setForDuration sets name to value and returns a func that restores
// whatever name held before the call (or leaves it unset, if it was unset),
// so a caller can compute what a function reading os.Getenv would resolve
// to under a DELIBERATELY CHOSEN value — never the calling process's own
// ambient one — without leaking the override past the call.
func setForDuration(name, value string) func() {
	orig, had := os.LookupEnv(name)
	_ = os.Setenv(name, value)
	return func() {
		if had {
			_ = os.Setenv(name, orig)
		} else {
			_ = os.Unsetenv(name)
		}
	}
}

// dialStatus dials path and calls broker/status under timeout: a read-only
// probe that proves a broker is not merely present but actually answering
// its own wire protocol. It never writes anything and never touches an
// existing broker's process.
func dialStatus(path string, timeout time.Duration) (reachable bool, sessions int, err error) {
	cli, dialErr := broker.Dial(path)
	if dialErr != nil {
		return false, 0, dialErr
	}
	defer func() { _ = cli.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	resp, statusErr := cli.Status(ctx)
	if statusErr != nil {
		return false, 0, statusErr
	}
	return true, len(resp.Sessions), nil
}

// checkReachability probes one candidate socket path and reports whether a
// broker answered broker/status there. Use this for a path whose
// reachability is ITSELF the thing being judged (the consumer path: if
// nothing answers there, real queries will fail — that is unconditionally
// bad).
func checkReachability(label, path string, timeout time.Duration) (result, bool) {
	name := fmt.Sprintf("%s reachable (%s)", label, path)
	reachable, sessions, err := dialStatus(path, timeout)
	if !reachable {
		return result{name: name, status: statusFail, detail: "not reachable: " + err.Error()}, false
	}
	return result{name: name, status: statusPass,
		detail: fmt.Sprintf("broker answered broker/status with %d warm session(s)", sessions)}, true
}

// checkReachabilityInfo probes a SECONDARY candidate path the same way
// checkReachability does, but always reports INFO regardless of outcome.
// Reachability of this path is not itself good or bad — it is context that
// feeds checkDivergence's verdict, which is where the actual judgement
// belongs. The single most common healthy deployment has this path
// UNREACHABLE (only the consumer path listening); styling that as FAIL
// would read as broken exactly when everything is correct.
func checkReachabilityInfo(label, path string, timeout time.Duration) (result, bool) {
	name := fmt.Sprintf("%s reachable (%s)", label, path)
	reachable, sessions, err := dialStatus(path, timeout)
	if !reachable {
		return result{name: name, status: statusInfo, detail: "not reachable: " + err.Error()}, false
	}
	return result{name: name, status: statusInfo,
		detail: fmt.Sprintf("broker answered broker/status with %d warm session(s)", sessions)}, true
}

// checkDivergence is the diagnosis this whole tool exists for: it names the
// exact failure recorded 2026-08-12 — a broker up and reachable at ONE
// candidate path while every real consumer dials the OTHER — rather than
// leaving it to be inferred from two reachability lines above it.
func checkDivergence(consumerPath, defaultPath string, consumerReachable, defaultReachable bool) result {
	const name = "socket-path divergence"
	if consumerPath == defaultPath {
		return result{name: name, status: statusPass,
			detail: "this environment's XDG_RUNTIME_DIR already matches the consumer's fallback resolution — no divergence possible here"}
	}
	switch {
	case defaultReachable && !consumerReachable:
		return result{name: name, status: statusFail, detail: fmt.Sprintf(
			"a broker IS listening at %s but mcp-ast/ae (spawned by mgk under a filtered env lacking "+
				"XDG_RUNTIME_DIR) will dial %s instead — every lsp-* call fails as \"broker not running\" "+
				"even though it is. Fix: reinstall the unit with `make install-service` (it pins -socket "+
				"to the consumer path), then `systemctl --user restart lspbridge.service`.",
			defaultPath, consumerPath)}
	case consumerReachable && defaultReachable:
		return result{name: name, status: statusWarn, detail: fmt.Sprintf(
			"both candidate paths are reachable (%s and %s) — most likely two broker processes running "+
				"at once. The consumer path works, which is what matters, but this wastes a warm session "+
				"and is worth tracking down.", consumerPath, defaultPath)}
	case consumerReachable:
		return result{name: name, status: statusPass,
			detail: "the consumer path is reachable and the XDG_RUNTIME_DIR path is not — expected, healthy state"}
	default:
		return result{name: name, status: statusPass,
			detail: "neither candidate path is reachable — see the reachability checks above; this is not a path-divergence problem"}
	}
}
