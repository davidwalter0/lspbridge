package server

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGtsLspSpecMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := GtsLspSpec("/some/dir")
	if err == nil {
		t.Fatal("expected error when gts-lsp is not on PATH")
	}
	if !strings.Contains(err.Error(), "exttool install --tool gts-lsp --confirm") {
		t.Errorf("error = %q lacks the install hint", err)
	}
}

// TestGtsLspSpecFound uses a stand-in executable (never spoken to over LSP),
// as the other presets' found-path tests do: it proves LookPath resolution and
// Dir plumbing, not the real server.
func TestGtsLspSpecFound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary PATH shim uses a Unix shebang script")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, GtsLspCommand)
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	spec, err := GtsLspSpec("/some/dir")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Command != fake || spec.Dir != "/some/dir" || len(spec.Args) != 0 {
		t.Errorf("spec = %+v", spec)
	}
}
