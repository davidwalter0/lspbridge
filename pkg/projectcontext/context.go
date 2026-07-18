// Package projectcontext defines the per-language project-root and config
// discovery seam that keys a warm LSP session in the broker.
//
// It generalizes the C/C++ include-context resolver: each language maps a
// source file to its project root and the config that scopes analysis
// (tsconfig.json, pubspec.yaml, compile_commands.json, pyrightconfig.json,
// Cargo.toml, go.mod). The broker keeps one warm session per [Context.Key].
package projectcontext

import (
	"os"
	"path/filepath"
	"slices"
)

// Context identifies the project and language scope of an LSP session.
type Context struct {
	// Root is the absolute project-root directory.
	Root string
	// Language is the LSP languageId (e.g. "python", "typescript").
	Language string
	// ConfigPath is the config file that established Root, if any.
	ConfigPath string
}

// Key is the stable session-cache key for the (Language, Root) pair. The
// broker keeps one warm LSP session per Key. The NUL separator cannot appear
// in a path, so distinct pairs never collide.
func (c Context) Key() string {
	return c.Language + "\x00" + c.Root
}

// Resolver maps an absolute source-file path to its project [Context] for one
// language. Resolve returns false when the resolver does not handle the file's
// language, letting the broker try the next resolver.
type Resolver interface {
	Resolve(absPath string) (Context, bool)
}

// FindUp walks from startDir upward (inclusive) to the filesystem root and
// returns the first directory containing any of the marker files, plus true.
// It returns "", false when no marker is found.
func FindUp(startDir string, markers ...string) (string, bool) {
	dir := startDir
	for {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
				return dir, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir { // reached the filesystem root
			return "", false
		}
		dir = parent
	}
}

// MarkerResolver resolves a file's project [Context] by walking up for any of
// its Markers. It handles files whose extension is in Extensions. When no
// marker is found it falls back to the file's own directory as the root.
type MarkerResolver struct {
	Language   string   // the LSP languageId to stamp on the Context
	Extensions []string // file extensions this resolver claims, e.g. []string{".py"}
	Markers    []string // root-marker filenames, most-specific first

	// LanguageFor, if non-nil, derives the LSP languageId from the matched
	// file's extension instead of the fixed Language above. This exists for
	// language families whose LSP languageId varies by extension within the
	// same resolver — e.g. TypeScript's JSX-flavored ".tsx" is
	// "typescriptreact", not "typescript" (see [TypeScriptResolver] /
	// [JavaScriptResolver]). Nil means every matched extension uses Language
	// unchanged, preserving every existing preset's behavior.
	LanguageFor func(ext string) string
}

// Resolve implements [Resolver].
func (r MarkerResolver) Resolve(absPath string) (Context, bool) {
	ext := filepath.Ext(absPath)
	if !slices.Contains(r.Extensions, ext) {
		return Context{}, false
	}
	lang := r.Language
	if r.LanguageFor != nil {
		lang = r.LanguageFor(ext)
	}
	start := filepath.Dir(absPath)
	root, ok := FindUp(start, r.Markers...)
	if !ok {
		root = start
	}
	cfg := ""
	for _, m := range r.Markers {
		p := filepath.Join(root, m)
		if _, err := os.Stat(p); err == nil {
			cfg = p
			break
		}
	}
	return Context{Root: root, Language: lang, ConfigPath: cfg}, true
}

// PythonResolver resolves Python files by pyright/project markers. It is a
// convenience preset for the P0 reference server (pyright).
func PythonResolver() MarkerResolver {
	return MarkerResolver{
		Language:   "python",
		Extensions: []string{".py", ".pyi"},
		Markers:    []string{"pyrightconfig.json", "pyproject.toml", "setup.py", "setup.cfg"},
	}
}

// nodeMarkers is the root-marker precedence shared by TypeScriptResolver and
// JavaScriptResolver: a tsconfig.json is the most specific signal (even in a
// JS project, e.g. one using `allowJs` for editor tooling), then a plain
// jsconfig.json, then — the loosest signal — a bare package.json.
var nodeMarkers = []string{"tsconfig.json", "jsconfig.json", "package.json"}

// TypeScriptResolver resolves TypeScript-family files (.ts, .tsx, .mts,
// .cts) by walking up for the first of nodeMarkers. The LSP languageId is
// "typescript" for every extension except ".tsx", which uses
// "typescriptreact" — the identifier the LSP spec and
// typescript-language-server expect for JSX-flavored TypeScript (verified
// against the LSP 3.17 specification's language-identifier table).
func TypeScriptResolver() MarkerResolver {
	return MarkerResolver{
		Language:   "typescript",
		Extensions: []string{".ts", ".tsx", ".mts", ".cts"},
		Markers:    nodeMarkers,
		LanguageFor: func(ext string) string {
			if ext == ".tsx" {
				return "typescriptreact"
			}
			return "typescript"
		},
	}
}

// JavaScriptResolver resolves JavaScript-family files (.js, .mjs, .cjs,
// .jsx) by the same marker precedence as [TypeScriptResolver] — a
// JavaScript-only project may still carry a tsconfig.json (for `allowJs`
// editor tooling) or a plain jsconfig.json, and either should still win over
// a bare package.json. The LSP languageId is "javascript" for every
// extension except ".jsx", which uses "javascriptreact" by the same
// reasoning TypeScriptResolver applies to ".tsx" (both identifiers are in
// the LSP spec's language-identifier table).
func JavaScriptResolver() MarkerResolver {
	return MarkerResolver{
		Language:   "javascript",
		Extensions: []string{".js", ".mjs", ".cjs", ".jsx"},
		Markers:    nodeMarkers,
		LanguageFor: func(ext string) string {
			if ext == ".jsx" {
				return "javascriptreact"
			}
			return "javascript"
		},
	}
}

// RustResolver resolves Rust files (.rs) by walking up for Cargo.toml, the
// crate/workspace root marker rust-analyzer keys its analysis on.
func RustResolver() MarkerResolver {
	return MarkerResolver{
		Language:   "rust",
		Extensions: []string{".rs"},
		Markers:    []string{"Cargo.toml"},
	}
}

// DefaultChain returns the resolver chain for every language lspbridge
// supports out of the box, tried in this order: Python, TypeScript,
// JavaScript, Rust. It is the broker's default Config.Resolver. Each
// resolver claims a disjoint Extensions set, so no file can match more than
// one — the order only affects readability, not correctness.
func DefaultChain() Chain {
	return Chain{PythonResolver(), TypeScriptResolver(), JavaScriptResolver(), RustResolver()}
}

// Chain tries each resolver in order and returns the first match.
type Chain []Resolver

// Resolve implements [Resolver] by delegating to the first member that claims
// the file.
func (c Chain) Resolve(absPath string) (Context, bool) {
	for _, r := range c {
		if ctx, ok := r.Resolve(absPath); ok {
			return ctx, true
		}
	}
	return Context{}, false
}
