package projectcontext

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeyDistinctPerLanguageAndRoot(t *testing.T) {
	a := Context{Root: "/proj", Language: "python"}
	b := Context{Root: "/proj", Language: "typescript"}
	c := Context{Root: "/other", Language: "python"}
	if a.Key() == b.Key() {
		t.Error("same root, different language collided")
	}
	if a.Key() == c.Key() {
		t.Error("same language, different root collided")
	}
	aAgain := Context{Root: "/proj", Language: "python"}
	if a.Key() != aAgain.Key() {
		t.Error("equal contexts produced different keys")
	}
}

func TestFindUp(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "a", "pyproject.toml")
	if err := os.WriteFile(marker, []byte("[project]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := FindUp(nested, "pyproject.toml")
	if !ok {
		t.Fatal("expected to find marker")
	}
	if got != filepath.Join(root, "a") {
		t.Errorf("root = %q, want %q", got, filepath.Join(root, "a"))
	}

	if _, ok := FindUp(nested, "nonexistent.marker"); ok {
		t.Error("expected no marker found")
	}
}

func TestMarkerResolver(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "pyrightconfig.json")
	if err := os.WriteFile(cfg, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(pkgDir, "mod.py")

	r := PythonResolver()
	ctx, ok := r.Resolve(srcPath)
	if !ok {
		t.Fatal("resolver did not claim .py file")
	}
	if ctx.Language != "python" {
		t.Errorf("language = %q", ctx.Language)
	}
	if ctx.Root != root {
		t.Errorf("root = %q, want %q", ctx.Root, root)
	}
	if ctx.ConfigPath != cfg {
		t.Errorf("config = %q, want %q", ctx.ConfigPath, cfg)
	}
}

func TestMarkerResolverRejectsOtherLanguages(t *testing.T) {
	r := PythonResolver()
	if _, ok := r.Resolve("/some/file.ts"); ok {
		t.Error("python resolver claimed a .ts file")
	}
}

func TestMarkerResolverFallsBackToFileDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "lone.py")
	if err := os.WriteFile(src, []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, ok := PythonResolver().Resolve(src)
	if !ok {
		t.Fatal("expected resolve")
	}
	if ctx.Root != dir {
		t.Errorf("fallback root = %q, want %q", ctx.Root, dir)
	}
	if ctx.ConfigPath != "" {
		t.Errorf("expected empty config, got %q", ctx.ConfigPath)
	}
}

func TestChain(t *testing.T) {
	ts := MarkerResolver{Language: "typescript", Extensions: []string{".ts"}, Markers: []string{"tsconfig.json"}}
	chain := Chain{PythonResolver(), ts}

	if ctx, ok := chain.Resolve("/x/a.ts"); !ok || ctx.Language != "typescript" {
		t.Errorf("chain did not route .ts to typescript resolver: %+v ok=%v", ctx, ok)
	}
	if _, ok := chain.Resolve("/x/a.rb"); ok {
		t.Error("chain claimed an unhandled extension")
	}
}
