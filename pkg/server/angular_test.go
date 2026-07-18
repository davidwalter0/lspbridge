package server

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestAngularLanguageServerSpecMissingBinary forces exec.LookPath to fail
// (an empty PATH) so the missing-binary branch is exercised
// deterministically regardless of what is installed on the host running
// the test. It also asserts the exttool remediation names
// "angular-language-server" (exttool's package-derived tool name), NOT
// "ngserver" (the resolved binary name) — see missingBinaryError's doc
// comment for why these differ for this preset alone.
func TestAngularLanguageServerSpecMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir on PATH: LookPath always fails
	_, err := AngularLanguageServerSpec("/some/dir")
	if err == nil {
		t.Fatal("expected error when ngserver is not on PATH")
	}
	const want = "exttool install --tool angular-language-server --confirm"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
	if strings.Contains(err.Error(), "--tool ngserver") {
		t.Errorf("error = %q, remediation must not name the bare binary %q", err.Error(), "ngserver")
	}
}

// TestAngularLanguageServerSpecFound is a FAKE-SERVER unit test, not a live
// proof — labeled as such because @angular/language-server is not
// available in a working state on the development host (see
// AngularLanguageServerSpec's doc comment: the one present is missing its
// required @angular/language-service peer). Unlike this package's
// pyright/typescript-language-server/rust-analyzer/dart-analysis-server
// equivalents, this test cannot skip-if-absent against a real installation
// and still exercise the "found" code path deterministically.
//
// Instead it writes a tiny stand-in executable named "ngserver" (a shell
// script that does nothing but exit 0 — a fake server, never spoken to
// over LSP) to a temp dir placed first on PATH, so exec.LookPath resolves
// to a KNOWN, controlled path regardless of host state. This proves the
// Spec-construction logic — LookPath resolution, Args assembly, Dir
// plumbing — end to end. It does NOT prove the real Angular Language
// Server accepts these args or speaks LSP correctly over them; that is
// exactly what "NO live test possible" means for this preset.
func TestAngularLanguageServerSpecFound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary PATH shim uses a Unix shebang script")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, AngularLanguageServerCommand)
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake ngserver: %v", err)
	}
	t.Setenv("PATH", dir)

	spec, err := AngularLanguageServerSpec("/some/dir")
	if err != nil {
		t.Fatalf("AngularLanguageServerSpec: %v", err)
	}
	if spec.Command != fake {
		t.Errorf("Command = %q, want %q", spec.Command, fake)
	}
	wantArgs := []string{"--stdio", "--tsProbeLocations", "/some/dir", "--ngProbeLocations", "/some/dir"}
	if !slices.Equal(spec.Args, wantArgs) {
		t.Errorf("Args = %v, want %v", spec.Args, wantArgs)
	}
	if spec.Dir != "/some/dir" {
		t.Errorf("Dir = %q, want /some/dir", spec.Dir)
	}
}
