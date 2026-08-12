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

func TestConsumerSocketPathIgnoresXDGRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1234")

	got := consumerSocketPath()

	if strings.Contains(got, "/run/user/1234") {
		t.Fatalf("consumerSocketPath must ignore XDG_RUNTIME_DIR, got %q", got)
	}
	if !strings.Contains(got, "lspbridge-") || !strings.HasSuffix(got, "broker.sock") {
		t.Fatalf("fallback path shape wrong: %q", got)
	}
	// consumerSocketPath must restore the env var for the rest of the
	// process (and this test) — it simulates the consumer's environment,
	// it does not adopt it.
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "/run/user/1234" {
		t.Fatalf("XDG_RUNTIME_DIR not restored after the call: %q", v)
	}
}

// TestConsumerSocketPathIgnoresTMPDIR pins the exact regression found on
// this host 2026-08-12: with XDG_RUNTIME_DIR unset (simulating the
// consumer) but TMPDIR set to something other than "/tmp" (as the
// developing shell's sandbox does — TMPDIR=/tmp/user/1001), a version of
// consumerSocketPath that unset only XDG_RUNTIME_DIR computed
// /tmp/user/1001/lspbridge-<uid>/... instead of /tmp/lspbridge-<uid>/... —
// the path the real, TMPDIR-less mgk-spawned consumer actually dials
// (verified against /proc/<mcp-ast-pid>/environ the same day). This test
// fails under that first-draft behavior and passes under the fix.
func TestConsumerSocketPathIgnoresTMPDIR(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp/user/1001")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1001")

	got := consumerSocketPath()

	if strings.Contains(got, "/tmp/user/1001") {
		t.Fatalf("consumerSocketPath must ignore the CALLER's TMPDIR, got %q", got)
	}
	if strings.Contains(got, "/run/user/1001") {
		t.Fatalf("consumerSocketPath must ignore XDG_RUNTIME_DIR too, got %q", got)
	}
	want := "/tmp/lspbridge-" + fmt.Sprint(os.Getuid()) + "/broker.sock"
	if got != want {
		t.Fatalf("consumerSocketPath() = %q, want %q (Go's os.TempDir() fallback when TMPDIR is unset)", got, want)
	}
}

func TestConsumerSocketPathWhenAlreadyUnset(t *testing.T) {
	orig, had := os.LookupEnv("XDG_RUNTIME_DIR")
	_ = os.Unsetenv("XDG_RUNTIME_DIR")
	defer func() {
		if had {
			_ = os.Setenv("XDG_RUNTIME_DIR", orig)
		}
	}()

	got := consumerSocketPath()

	if _, stillSet := os.LookupEnv("XDG_RUNTIME_DIR"); stillSet {
		t.Fatal("XDG_RUNTIME_DIR should remain unset when it started unset")
	}
	if !strings.Contains(got, "lspbridge-") {
		t.Fatalf("got %q", got)
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
