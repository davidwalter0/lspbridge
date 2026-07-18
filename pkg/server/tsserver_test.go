package server

import (
	"os/exec"
	"strings"
	"testing"
)

// TestTypeScriptLanguageServerSpecMissingBinary forces exec.LookPath to fail
// (an empty PATH) so the missing-binary branch is exercised deterministically
// regardless of what is installed on the host running the test.
func TestTypeScriptLanguageServerSpecMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir on PATH: LookPath always fails
	_, err := TypeScriptLanguageServerSpec("/some/dir")
	if err == nil {
		t.Fatal("expected error when typescript-language-server is not on PATH")
	}
	const want = "exttool install --tool typescript-language-server --confirm"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
}

// TestTypeScriptLanguageServerSpecFound asserts the Spec shape when the
// binary is actually resolvable; skipped (not failed) otherwise, the same
// convention pyright's tests use.
func TestTypeScriptLanguageServerSpecFound(t *testing.T) {
	path, err := exec.LookPath(TypeScriptLanguageServerCommand)
	if err != nil {
		t.Skipf("typescript-language-server not installed (%v)", err)
	}
	spec, err := TypeScriptLanguageServerSpec("/some/dir")
	if err != nil {
		t.Fatalf("TypeScriptLanguageServerSpec: %v", err)
	}
	if spec.Command != path {
		t.Errorf("Command = %q, want %q", spec.Command, path)
	}
	if len(spec.Args) != 1 || spec.Args[0] != "--stdio" {
		t.Errorf("Args = %v, want [--stdio]", spec.Args)
	}
	if spec.Dir != "/some/dir" {
		t.Errorf("Dir = %q, want /some/dir", spec.Dir)
	}
}
