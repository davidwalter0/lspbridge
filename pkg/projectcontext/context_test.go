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

// TestMarkerResolverLanguageFor exercises the LanguageFor override directly
// (independent of any concrete preset), including that a nil LanguageFor
// leaves every prior MarkerResolver user's behavior unchanged.
func TestMarkerResolverLanguageFor(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.foo")
	if err := os.WriteFile(src, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	withOverride := MarkerResolver{
		Language:   "base",
		Extensions: []string{".foo"},
		LanguageFor: func(ext string) string {
			return "override:" + ext
		},
	}
	ctx, ok := withOverride.Resolve(src)
	if !ok || ctx.Language != "override:.foo" {
		t.Errorf("LanguageFor override: ctx = %+v, ok = %v, want language %q", ctx, ok, "override:.foo")
	}

	withoutOverride := MarkerResolver{Language: "base", Extensions: []string{".foo"}}
	ctx, ok = withoutOverride.Resolve(src)
	if !ok || ctx.Language != "base" {
		t.Errorf("nil LanguageFor: ctx = %+v, ok = %v, want language %q", ctx, ok, "base")
	}
}

func TestTypeScriptResolver(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "tsconfig.json")
	if err := os.WriteFile(cfg, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := TypeScriptResolver()

	cases := []struct {
		file, wantLang string
	}{
		{"a.ts", "typescript"},
		{"a.mts", "typescript"},
		{"a.cts", "typescript"},
		{"a.tsx", "typescriptreact"},
	}
	for _, c := range cases {
		ctx, ok := r.Resolve(filepath.Join(root, c.file))
		if !ok {
			t.Fatalf("%s: resolver did not claim file", c.file)
		}
		if ctx.Language != c.wantLang {
			t.Errorf("%s: language = %q, want %q", c.file, ctx.Language, c.wantLang)
		}
		if ctx.Root != root {
			t.Errorf("%s: root = %q, want %q", c.file, ctx.Root, root)
		}
		if ctx.ConfigPath != cfg {
			t.Errorf("%s: config = %q, want %q", c.file, ctx.ConfigPath, cfg)
		}
	}
	if _, ok := r.Resolve(filepath.Join(root, "a.js")); ok {
		t.Error("TypeScriptResolver claimed a .js file")
	}
}

func TestTypeScriptResolverMarkerPrecedence(t *testing.T) {
	root := t.TempDir()
	// Both tsconfig.json and package.json present: tsconfig.json must win
	// (most-specific first), even though package.json is also a valid marker.
	tsconfig := filepath.Join(root, "tsconfig.json")
	if err := os.WriteFile(tsconfig, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, ok := TypeScriptResolver().Resolve(filepath.Join(root, "a.ts"))
	if !ok {
		t.Fatal("expected resolve")
	}
	if ctx.ConfigPath != tsconfig {
		t.Errorf("config = %q, want tsconfig.json to win over package.json", ctx.ConfigPath)
	}
}

func TestJavaScriptResolver(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "jsconfig.json")
	if err := os.WriteFile(cfg, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := JavaScriptResolver()

	cases := []struct {
		file, wantLang string
	}{
		{"a.js", "javascript"},
		{"a.mjs", "javascript"},
		{"a.cjs", "javascript"},
		{"a.jsx", "javascriptreact"},
	}
	for _, c := range cases {
		ctx, ok := r.Resolve(filepath.Join(root, c.file))
		if !ok {
			t.Fatalf("%s: resolver did not claim file", c.file)
		}
		if ctx.Language != c.wantLang {
			t.Errorf("%s: language = %q, want %q", c.file, ctx.Language, c.wantLang)
		}
		if ctx.ConfigPath != cfg {
			t.Errorf("%s: config = %q, want %q", c.file, ctx.ConfigPath, cfg)
		}
	}
	if _, ok := r.Resolve(filepath.Join(root, "a.ts")); ok {
		t.Error("JavaScriptResolver claimed a .ts file")
	}
}

func TestJavaScriptResolverFallsBackToPackageJSON(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "package.json")
	if err := os.WriteFile(pkg, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, ok := JavaScriptResolver().Resolve(filepath.Join(root, "a.js"))
	if !ok {
		t.Fatal("expected resolve")
	}
	if ctx.ConfigPath != pkg {
		t.Errorf("config = %q, want package.json fallback %q", ctx.ConfigPath, pkg)
	}
}

func TestRustResolver(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "Cargo.toml")
	if err := os.WriteFile(cfg, []byte("[package]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcDir := filepath.Join(root, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(srcDir, "main.rs")

	ctx, ok := RustResolver().Resolve(src)
	if !ok {
		t.Fatal("resolver did not claim .rs file")
	}
	if ctx.Language != "rust" {
		t.Errorf("language = %q, want rust", ctx.Language)
	}
	if ctx.Root != root {
		t.Errorf("root = %q, want %q", ctx.Root, root)
	}
	if ctx.ConfigPath != cfg {
		t.Errorf("config = %q, want %q", ctx.ConfigPath, cfg)
	}
	if _, ok := RustResolver().Resolve("/x/a.py"); ok {
		t.Error("RustResolver claimed a .py file")
	}
}

func TestDefaultChain(t *testing.T) {
	chain := DefaultChain()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		file, wantLang string
	}{
		{"a.py", "python"},
		{"a.ts", "typescript"},
		{"a.tsx", "typescriptreact"},
		{"a.js", "javascript"},
		{"a.jsx", "javascriptreact"},
	}
	for _, c := range cases {
		ctx, ok := chain.Resolve(filepath.Join(root, c.file))
		if !ok || ctx.Language != c.wantLang {
			t.Errorf("%s: chain resolved %+v ok=%v, want language %q", c.file, ctx, ok, c.wantLang)
		}
	}

	// Rust needs its own marker (Cargo.toml), not tsconfig.json, so exercise
	// it in a separate root.
	rustRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(rustRoot, "Cargo.toml"), []byte("[package]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, ok := chain.Resolve(filepath.Join(rustRoot, "main.rs"))
	if !ok || ctx.Language != "rust" {
		t.Errorf("main.rs: chain resolved %+v ok=%v, want language rust", ctx, ok)
	}

	if _, ok := chain.Resolve(filepath.Join(root, "a.rb")); ok {
		t.Error("chain claimed an unhandled extension (.rb)")
	}
}
