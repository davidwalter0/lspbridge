package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestClangdSpecMissingBinary forces exec.LookPath to fail (an empty PATH)
// so the missing-binary branch is exercised deterministically regardless of
// what is installed on the host running the test.
func TestClangdSpecMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir on PATH: LookPath always fails
	_, err := ClangdSpec("/some/dir")
	if err == nil {
		t.Fatal("expected error when clangd is not on PATH")
	}
	const want = "exttool install --tool clangd --confirm"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
}

// TestClangdSpecFoundWithCompileCommands asserts the Spec shape when clangd
// is resolvable AND dir contains a compile_commands.json: the
// --compile-commands-dir flag must point at dir explicitly (see
// ClangdSpec's doc comment for why this Spec never relies on clangd's own
// implicit upward search in this branch). Skipped, not failed, when clangd
// isn't installed — the same convention every other preset's test in this
// package uses.
func TestClangdSpecFoundWithCompileCommands(t *testing.T) {
	path, err := exec.LookPath(ClangdCommand)
	if err != nil {
		t.Skipf("clangd not installed (%v)", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compile_commands.json"), []byte("[]"), 0o644); err != nil {
		t.Fatalf("write compile_commands.json: %v", err)
	}

	spec, err := ClangdSpec(dir)
	if err != nil {
		t.Fatalf("ClangdSpec: %v", err)
	}
	if spec.Command != path {
		t.Errorf("Command = %q, want %q", spec.Command, path)
	}
	wantArg := "--compile-commands-dir=" + dir
	if len(spec.Args) != 1 || spec.Args[0] != wantArg {
		t.Errorf("Args = %v, want [%s]", spec.Args, wantArg)
	}
	if spec.Dir != dir {
		t.Errorf("Dir = %q, want %q", spec.Dir, dir)
	}
}

// TestClangdSpecFoundWithoutCompileCommands asserts the OTHER half of
// ClangdSpec's conditional: when dir has no compile_commands.json, the
// --compile-commands-dir flag must be omitted entirely rather than pointed
// at an empty directory (see the doc comment on why: an unverified guess
// about clangd's behavior in that case is a regression risk this Spec
// declines to take).
func TestClangdSpecFoundWithoutCompileCommands(t *testing.T) {
	path, err := exec.LookPath(ClangdCommand)
	if err != nil {
		t.Skipf("clangd not installed (%v)", err)
	}
	dir := t.TempDir() // deliberately no compile_commands.json written

	spec, err := ClangdSpec(dir)
	if err != nil {
		t.Fatalf("ClangdSpec: %v", err)
	}
	if spec.Command != path {
		t.Errorf("Command = %q, want %q", spec.Command, path)
	}
	if len(spec.Args) != 0 {
		t.Errorf("Args = %v, want none", spec.Args)
	}
	if spec.Dir != dir {
		t.Errorf("Dir = %q, want %q", spec.Dir, dir)
	}
}
