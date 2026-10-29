package gtsfallback

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/davidwalter0/lspbridge/pkg/projectcontext"
	"github.com/davidwalter0/lspbridge/pkg/server"
)

// fakeGts writes a stand-in gts-lsp that answers the two probe flags.
func fakeGts(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell shim")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "gts-lsp")
	script := `#!/bin/sh
case "$1" in
  --languages) echo '["bash","go","yaml"]' ;;
  --detect) case "$2" in
      *.yaml) echo yaml ;;
      *.go) echo go ;;
      *) exit 1 ;;
    esac ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return p
}

func TestDetectAndSupports(t *testing.T) {
	bin := fakeGts(t)
	f := &Fallback{Binary: bin}
	if lang, ok := f.Detect("/p/values.yaml"); !ok || lang != "yaml" {
		t.Errorf("Detect yaml = %q %v", lang, ok)
	}
	if _, ok := f.Detect("/p/blob.xyz"); ok {
		t.Error("unclaimed file was detected")
	}
	if !f.Supports("go") || f.Supports("python") || !f.Supports("shellscript") { // shellscript aliases to bash
		t.Errorf("Supports: go=%v python=%v shellscript=%v", f.Supports("go"), f.Supports("python"), f.Supports("shellscript"))
	}
	// An absent binary declines everything.
	none := &Fallback{Binary: "/does/not/exist"}
	if _, ok := none.Detect("/p/a.yaml"); ok {
		t.Error("missing binary must decline")
	}
	if none.Supports("go") {
		t.Error("missing binary supports nothing")
	}
}

func TestChainDedicatedWinsFallbackLast(t *testing.T) {
	f := &Fallback{Binary: fakeGts(t)}
	dir := t.TempDir()
	if ctx, ok := f.Chain().Resolve(filepath.Join(dir, "a.py")); !ok || ctx.Language != "python" {
		t.Errorf("a.py -> %+v", ctx)
	}
	if ctx, ok := f.Chain().Resolve(filepath.Join(dir, "main.go")); !ok || ctx.Language != "go" {
		t.Errorf("main.go -> %+v", ctx)
	}
	if _, ok := f.Chain().Resolve(filepath.Join(dir, "blob.xyz")); ok {
		t.Error("blob.xyz claimed")
	}
}

func TestSpecForPrecedence(t *testing.T) {
	f := &Fallback{Binary: fakeGts(t)}
	dedicatedErr := errors.New("dedicated: not available")
	dedicated := func(pc projectcontext.Context) (server.Spec, error) {
		if pc.Language == "have" {
			return server.Spec{Command: "dedicated"}, nil
		}
		return server.Spec{}, dedicatedErr
	}
	sf := f.SpecFor(dedicated)

	if spec, err := sf(projectcontext.Context{Language: "have", Root: "/r"}); err != nil || spec.Command != "dedicated" {
		t.Errorf("dedicated must win: %+v %v", spec, err)
	}
	spec, err := sf(projectcontext.Context{Language: "go", Root: "/r"})
	if err != nil || !strings.HasSuffix(spec.Command, "gts-lsp") || spec.Dir != "/r" {
		t.Errorf("go should fall back to gts-lsp: %+v %v", spec, err)
	}
	// A language gts has no grammar for keeps the ORIGINAL error.
	if _, err := sf(projectcontext.Context{Language: "klingon", Root: "/r"}); !errors.Is(err, dedicatedErr) {
		t.Errorf("unclaimed language error = %v, want the dedicated error unchanged", err)
	}
	// The broker's own default error text for unclaimed languages is preserved.
	def := f.SpecFor(nil)
	_, err = def(projectcontext.Context{Language: "klingon", Root: "/r"})
	if err == nil || err.Error() != `broker: no language server configured for "klingon"` {
		t.Errorf("default error = %v", err)
	}
}
