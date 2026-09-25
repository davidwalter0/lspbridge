package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestStatusString(t *testing.T) {
	cases := map[status]string{
		statusPass: "PASS",
		statusWarn: "WARN",
		statusFail: "FAIL",
		statusSkip: "SKIP",
		statusInfo: "INFO",
		status(99): "?",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("status(%d).String() = %q, want %q", s, got, want)
		}
	}
}

func TestResultString(t *testing.T) {
	r := result{name: "thing", status: statusPass}
	if got := r.String(); got != "[PASS] thing" {
		t.Errorf("no-detail form: got %q", got)
	}
	r.detail = "extra"
	if got := r.String(); got != "[PASS] thing: extra" {
		t.Errorf("with-detail form: got %q", got)
	}
}

// TestConsumerSocketPathUsesUIDRuntimeDirRegardlessOfAmbientEnv pins the
// 2026-09-25 re-measurement: both real consumers (mcp-ast,
// mcp-agent-editor) now carry XDG_RUNTIME_DIR=/run/user/<uid> in their
// spawn environment (verified live against /proc/<pid>/environ for both),
// so consumerSocketPath must land on that path — and it must do so by
// FORCING the uid-derived value, never by trusting whatever the calling
// process's own ambient XDG_RUNTIME_DIR/TMPDIR happen to be, which is the
// same "wrong instrument" class of bug the superseded
// TestConsumerSocketPathIgnoresTMPDIR pinned for the 2026-08-12 behavior
// (see checks.go's consumerSocketPath doc comment).
func TestConsumerSocketPathUsesUIDRuntimeDirRegardlessOfAmbientEnv(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1234") // a bogus, unrelated ambient value
	t.Setenv("TMPDIR", "/tmp/user/1001")          // must not matter once XDG_RUNTIME_DIR is forced

	got := consumerSocketPath()

	want := fmt.Sprintf("/run/user/%d/lspbridge/broker.sock", os.Getuid())
	if got != want {
		t.Fatalf("consumerSocketPath() = %q, want %q", got, want)
	}
}

// TestConsumerSocketPathRestoresAmbientXDGRuntimeDir verifies the override
// is process-local and does not leak past the call, mirroring the
// restoration guarantee the superseded unset-based implementation made.
func TestConsumerSocketPathRestoresAmbientXDGRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1234")

	_ = consumerSocketPath()

	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "/run/user/1234" {
		t.Fatalf("XDG_RUNTIME_DIR not restored after the call: %q", v)
	}
}

// TestConsumerSocketPathWhenAmbientXDGRuntimeDirUnset confirms the forced
// value and the restore-to-unset path both hold even when the CALLING
// process happens to have no XDG_RUNTIME_DIR at all (e.g. a bare script
// with no login-session environment) — doctor's answer must not depend on
// it either way.
func TestConsumerSocketPathWhenAmbientXDGRuntimeDirUnset(t *testing.T) {
	orig, had := os.LookupEnv("XDG_RUNTIME_DIR")
	_ = os.Unsetenv("XDG_RUNTIME_DIR")
	defer func() {
		if had {
			_ = os.Setenv("XDG_RUNTIME_DIR", orig)
		}
	}()

	got := consumerSocketPath()

	want := fmt.Sprintf("/run/user/%d/lspbridge/broker.sock", os.Getuid())
	if got != want {
		t.Fatalf("consumerSocketPath() = %q, want %q", got, want)
	}
	if _, stillSet := os.LookupEnv("XDG_RUNTIME_DIR"); stillSet {
		t.Fatal("XDG_RUNTIME_DIR should remain unset after the call when it started unset")
	}
}

func TestCheckDivergence(t *testing.T) {
	t.Run("paths equal is always PASS regardless of reachability", func(t *testing.T) {
		r := checkDivergence("/a", "/a", false, false)
		if r.status != statusPass {
			t.Fatalf("status = %v, want PASS", r.status)
		}
	})

	t.Run("the incident: default reachable, consumer not, is FAIL", func(t *testing.T) {
		r := checkDivergence("/consumer", "/default", false, true)
		if r.status != statusFail {
			t.Fatalf("status = %v, want FAIL", r.status)
		}
		if !strings.Contains(r.detail, "/default") || !strings.Contains(r.detail, "/consumer") {
			t.Fatalf("detail should name both paths: %s", r.detail)
		}
		if !strings.Contains(r.detail, "install-service") {
			t.Fatalf("detail should name the remedy: %s", r.detail)
		}
	})

	t.Run("both reachable is WARN, not FAIL — consumer path still works", func(t *testing.T) {
		r := checkDivergence("/consumer", "/default", true, true)
		if r.status != statusWarn {
			t.Fatalf("status = %v, want WARN", r.status)
		}
	})

	t.Run("only consumer reachable is the healthy PASS state", func(t *testing.T) {
		r := checkDivergence("/consumer", "/default", true, false)
		if r.status != statusPass {
			t.Fatalf("status = %v, want PASS", r.status)
		}
	})

	t.Run("neither reachable is PASS on divergence specifically (broker is just down)", func(t *testing.T) {
		r := checkDivergence("/consumer", "/default", false, false)
		if r.status != statusPass {
			t.Fatalf("status = %v, want PASS", r.status)
		}
	})
}
