// Package packaging holds the systemd --user unit TEMPLATE
// (lspbridge.service) that make install-service renders. This file's test
// renders it for real, into a throwaway SYSTEMD_USER_DIR/GOBIN, and reads
// back the ExecStart line — the actual observable this whole design exists
// to get right, per docs/design/broker-lifecycle-and-socket-path.org: a
// unit whose -socket flag matches what the real consumers (mcp-ast,
// mcp-agent-editor) resolve.
//
// It never touches the real ~/.config or a real systemd instance: every
// path involved (GOBIN, XDG_CONFIG_HOME/SYSTEMD_USER_DIR) is overridden to
// a t.TempDir(), exactly the verification recipe the Makefile's own comment
// documents ("make install-service GOBIN=/tmp/x/bin
// XDG_CONFIG_HOME=/tmp/x/config"). No systemctl call is made anywhere in
// this file.
package packaging

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// findRepoRoot walks up from the current package directory looking for the
// Makefile that owns the install-service target, rather than assuming a
// fixed depth — the exact "don't hand-derive a path, ask the filesystem"
// discipline this repo's other packaging comments apply to environment
// variables.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "Makefile")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "packaging", "lspbridge.service")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate repo root (Makefile + packaging/lspbridge.service) above %s", dir)
		}
		dir = parent
	}
}

// renderUnit runs `make install-service` with GOBIN and SYSTEMD_USER_DIR
// pointed at throwaway directories under t.TempDir(), plus any extra
// Make variable overrides (e.g. LSPBROKER_SOCKET=...), and returns the
// rendered unit file's content. It never runs systemctl and never writes
// under the real $HOME.
func renderUnit(t *testing.T, extraVars ...string) string {
	t.Helper()
	root := findRepoRoot(t)

	tmp := t.TempDir()
	gobin := filepath.Join(tmp, "bin")
	systemdUserDir := filepath.Join(tmp, "systemd-user")

	args := []string{
		"install-service",
		"GOBIN=" + gobin,
		"SYSTEMD_USER_DIR=" + systemdUserDir,
	}
	args = append(args, extraVars...)

	cmd := exec.Command("make", args...)
	cmd.Dir = root
	// make install-service builds bin/lspbroker as a prerequisite; give it
	// real room on a shared, possibly loaded machine rather than a tight
	// deadline (see the process-isolation convention: bound, don't kill).
	done := make(chan error, 1)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start make install-service: %v", err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("make install-service failed: %v\n%s", err, out.String())
		}
	case <-time.After(180 * time.Second):
		_ = cmd.Process.Kill() // this test's own child only, not a broad kill
		t.Fatalf("make install-service timed out after 180s\n%s", out.String())
	}

	unitPath := filepath.Join(systemdUserDir, "lspbridge.service")
	content, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read rendered unit %s: %v (make output: %s)", unitPath, err, out.String())
	}
	return string(content)
}

// execStartLine extracts the ExecStart= line from a rendered unit's
// [Service] section.
func execStartLine(t *testing.T, unit string) string {
	t.Helper()
	sc := bufio.NewScanner(strings.NewReader(unit))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "ExecStart=") {
			return line
		}
	}
	t.Fatalf("no ExecStart= line found in rendered unit:\n%s", unit)
	return ""
}

func TestInstallServiceRendersExpectedSocketFlag(t *testing.T) {
	cases := []struct {
		name       string
		extraVars  []string
		wantSocket string
	}{
		{
			// The default: LSPBROKER_SOCKET's Makefile default,
			// %t/lspbridge/broker.sock -- a literal systemd specifier
			// that expands to $XDG_RUNTIME_DIR only when systemd itself
			// parses the unit (systemd.unit(5)), so the rendered FILE is
			// expected to carry the specifier UNEXPANDED.
			name:       "default pins the %t specifier, matching what the real consumers resolve",
			extraVars:  nil,
			wantSocket: "%t/lspbridge/broker.sock",
		},
		{
			// LSPBROKER_SOCKET must stay overridable -- e.g. for a host
			// where a consumer's spawn environment has diverged again
			// (see the packaging/lspbridge.service "IF THE CONSUMER'S
			// ENVIRONMENT DIVERGES" note) and a literal path is needed
			// instead of the specifier.
			name:       "LSPBROKER_SOCKET override survives the render",
			extraVars:  []string{"LSPBROKER_SOCKET=/custom/test/broker.sock"},
			wantSocket: "/custom/test/broker.sock",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unit := renderUnit(t, tc.extraVars...)
			got := execStartLine(t, unit)
			want := "ExecStart=" // prefix check first for a clearer failure
			if !strings.HasPrefix(got, want) {
				t.Fatalf("ExecStart line = %q, want prefix %q", got, want)
			}
			if !strings.Contains(got, "-socket "+tc.wantSocket) {
				t.Fatalf("ExecStart line = %q, want it to contain \"-socket %s\"", got, tc.wantSocket)
			}
			// No unsubstituted placeholder tokens should ever reach the
			// rendered file -- a leftover __SOCKET__/__EXEC__/__PATH__
			// means the sed substitution silently failed to match.
			for _, placeholder := range []string{"__SOCKET__", "__EXEC__", "__PATH__"} {
				if strings.Contains(unit, placeholder) {
					t.Fatalf("rendered unit still contains unsubstituted placeholder %q:\n%s", placeholder, unit)
				}
			}
		})
	}
}
