package server

import (
	"os/exec"
	"strings"
	"testing"
)

// TestRustAnalyzerSpecMissingBinary forces exec.LookPath to fail (an empty
// PATH) so the missing-binary branch is exercised deterministically
// regardless of what is installed on the host running the test.
func TestRustAnalyzerSpecMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir on PATH: LookPath always fails
	_, err := RustAnalyzerSpec("/some/dir")
	if err == nil {
		t.Fatal("expected error when rust-analyzer is not on PATH")
	}
	const want = "exttool install --tool rust-analyzer --confirm"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
}

// TestRustAnalyzerSpecFound asserts the Spec shape when the binary is
// actually resolvable; skipped (not failed) otherwise, the same convention
// pyright's tests use.
func TestRustAnalyzerSpecFound(t *testing.T) {
	path, err := exec.LookPath(RustAnalyzerCommand)
	if err != nil {
		t.Skipf("rust-analyzer not installed (%v)", err)
	}
	spec, err := RustAnalyzerSpec("/some/dir")
	if err != nil {
		t.Fatalf("RustAnalyzerSpec: %v", err)
	}
	if spec.Command != path {
		t.Errorf("Command = %q, want %q", spec.Command, path)
	}
	if len(spec.Args) != 0 {
		t.Errorf("Args = %v, want none", spec.Args)
	}
	if spec.Dir != "/some/dir" {
		t.Errorf("Dir = %q, want /some/dir", spec.Dir)
	}
}
