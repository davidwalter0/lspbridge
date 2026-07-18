package server

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDartAnalysisServerSpecMissingBinary forces exec.LookPath to fail (an
// empty PATH) so the missing-binary branch is exercised deterministically
// regardless of what is installed on the host running the test.
func TestDartAnalysisServerSpecMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir on PATH: LookPath always fails
	_, err := DartAnalysisServerSpec("/some/dir")
	if err == nil {
		t.Fatal("expected error when dart is not on PATH")
	}
	const want = "exttool install --tool dart --confirm"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
}

// TestDartAnalysisServerSpecFound asserts the Spec shape when the binary is
// actually resolvable; skipped (not failed) otherwise, the same convention
// pyright's/rust-analyzer's tests use.
func TestDartAnalysisServerSpecFound(t *testing.T) {
	path, err := exec.LookPath(DartCommand)
	if err != nil {
		t.Skipf("dart not installed (%v)", err)
	}
	spec, err := DartAnalysisServerSpec("/some/dir")
	if err != nil {
		t.Fatalf("DartAnalysisServerSpec: %v", err)
	}
	if spec.Command != path {
		t.Errorf("Command = %q, want %q", spec.Command, path)
	}
	if len(spec.Args) != 2 || spec.Args[0] != "language-server" || spec.Args[1] != "--protocol=lsp" {
		t.Errorf("Args = %v, want [language-server --protocol=lsp]", spec.Args)
	}
	if spec.Dir != "/some/dir" {
		t.Errorf("Dir = %q, want /some/dir", spec.Dir)
	}
}
