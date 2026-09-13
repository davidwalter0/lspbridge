package server

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBashLanguageServerSpecMissingBinary forces exec.LookPath to fail (an
// empty PATH) so the missing-binary branch is exercised deterministically
// regardless of what is installed on the host running the test. This is
// also the ONLY branch of BashLanguageServerSpec that is genuinely exercised
// on the development host: bash-language-server is not installed there (see
// BashLanguageServerSpec's doc comment).
func TestBashLanguageServerSpecMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir on PATH: LookPath always fails
	_, err := BashLanguageServerSpec("/some/dir")
	if err == nil {
		t.Fatal("expected error when bash-language-server is not on PATH")
	}
	const want = "exttool install --tool bash-language-server --confirm"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
}

// TestBashLanguageServerSpecFound is a FAKE-SERVER unit test, not a live
// proof — labeled as such because bash-language-server is not installed on
// the development host (see BashLanguageServerSpec's doc comment). Like
// [AngularLanguageServerSpec]'s equivalent test, it cannot skip-if-absent
// against a real installation and still exercise the "found" code path
// deterministically, so it writes a tiny stand-in executable named
// "bash-language-server" (a shell script that does nothing but exit 0 — a
// fake server, never spoken to over LSP) to a temp dir placed first on
// PATH. This proves the Spec-construction logic — LookPath resolution, Args
// assembly, Dir plumbing — end to end. It does NOT prove the real
// bash-language-server accepts "start" and speaks LSP correctly over it;
// that argv was instead verified by reading the project's own CLI source
// (see BashLanguageServerSpec's doc comment) rather than by a live run.
func TestBashLanguageServerSpecFound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary PATH shim uses a Unix shebang script")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, BashLanguageServerCommand)
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake bash-language-server: %v", err)
	}
	t.Setenv("PATH", dir)

	spec, err := BashLanguageServerSpec("/some/dir")
	if err != nil {
		t.Fatalf("BashLanguageServerSpec: %v", err)
	}
	if spec.Command != fake {
		t.Errorf("Command = %q, want %q", spec.Command, fake)
	}
	if len(spec.Args) != 1 || spec.Args[0] != "start" {
		t.Errorf("Args = %v, want [start]", spec.Args)
	}
	if spec.Dir != "/some/dir" {
		t.Errorf("Dir = %q, want /some/dir", spec.Dir)
	}
}
