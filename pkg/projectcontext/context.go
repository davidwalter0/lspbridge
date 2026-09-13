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

	// RequireMarker, when true, makes a discoverable marker mandatory: if
	// none of Markers is found walking up from the file, Resolve reports no
	// match at all instead of falling back to the file's own directory.
	// The zero value (false) preserves every prior preset's
	// fallback-to-own-directory behavior unchanged. This exists for
	// extensions that are not inherently tied to one language server on
	// their own — e.g. ".html" is only Angular's template extension inside
	// an Angular CLI workspace (see [AngularResolver]); an ordinary static
	// .html file with no angular.json above it is not this resolver's
	// concern, and must be left unclaimed rather than misrouted to
	// ngserver with a bogus fallback root.
	RequireMarker bool
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
		if r.RequireMarker {
			return Context{}, false
		}
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

// DartResolver resolves Dart files (.dart) by walking up for pubspec.yaml,
// the pub package root marker the Dart Analysis Server keys its analysis on
// (mirrors [RustResolver]'s Cargo.toml / [TypeScriptResolver]'s
// tsconfig.json). The LSP languageId is "dart" — the identifier the Dart
// Analysis Server's LSP handler and the wider LSP ecosystem (e.g. the
// language-identifier table VS Code's Dart extension advertises) use for
// this extension.
func DartResolver() MarkerResolver {
	return MarkerResolver{
		Language:   "dart",
		Extensions: []string{".dart"},
		Markers:    []string{"pubspec.yaml"},
	}
}

// AngularResolver resolves Angular template files (.html) by walking up for
// angular.json, the Angular CLI workspace marker. Unlike every other
// MarkerResolver preset above, plain ".html" is not inherently tied to one
// language server: an .html file with no Angular workspace above it is
// ordinary markup, not an Angular template, so RequireMarker is set —
// absent a discoverable angular.json, Resolve reports no match at all
// (rather than falling back to the file's own directory), leaving the file
// unclaimed by this preset instead of misrouting a plain static HTML file
// to ngserver.
//
// The LSP languageId is "html", not "angular" — verified directly against
// the installed @angular/language-server v20.0.1's own bundled source
// (`LanguageId2["HTML"] = "html"`, and its documentSymbol/didOpen handling
// keys off `params.textDocument.uri.endsWith(".html")`), and independently
// confirmed by Angular's official Language Service docs
// (https://angular.dev/tools/language-service) and the lsp-mode Angular
// client (https://emacs-lsp.github.io/lsp-mode/page/lsp-angular/), both of
// which register this client for languageIds "ts" / "typescript" / "html".
// "angular" is not a recognized LSP languageId anywhere in that chain.
func AngularResolver() MarkerResolver {
	return MarkerResolver{
		Language:      "html",
		Extensions:    []string{".html"},
		Markers:       []string{"angular.json"},
		RequireMarker: true,
	}
}

// cMarkers is the root-marker list shared by CResolver and CppResolver: a
// compile_commands.json is the JSON compilation database clangd consults
// for per-file compiler flags (see server.ClangdSpec's doc comment for how
// it is passed through once resolved).
var cMarkers = []string{"compile_commands.json"}

// CResolver resolves C source/header files (.c, .h) by walking up for
// compile_commands.json. A bare ".h" header is inherently ambiguous between
// C and C++ — nothing in the filename or a marker file resolves that
// without reading the compilation database's own per-file flags — so this
// resolver's default, like most C tooling, is that ".h" is a C header; see
// [CppResolver] for the C++-only extensions.
//
// Not yet wired into [DefaultChain] / the broker's DefaultSpecFor switch
// (package pkg/broker, outside projectcontext): this resolver exists so a
// caller can use projectcontext.CResolver().Resolve(path) directly (as
// server.ClangdSpec's tests do) ahead of that wiring, which is a separate,
// deliberately out-of-scope change.
func CResolver() MarkerResolver {
	return MarkerResolver{
		Language:   "c",
		Extensions: []string{".c", ".h"},
		Markers:    cMarkers,
	}
}

// CppResolver resolves C++ source/header files (.cc, .cpp, .cxx, .c++, .hh,
// .hpp, .hxx, .h++) by the same compile_commands.json marker as
// [CResolver]. The LSP languageId is "cpp" — the identifier both the LSP
// ecosystem and clangd itself expect (verified against the VS Code / LSP
// language-identifier table, the same source [TypeScriptResolver]'s doc
// comment cites for "typescriptreact").
//
// This is a separate function from CResolver — despite sharing cMarkers —
// mirroring how [TypeScriptResolver] / [JavaScriptResolver] are separate
// despite sharing nodeMarkers: one LSP languageId per resolver, even when
// the marker set and the backing server are identical. See CResolver's doc
// comment for the DefaultChain wiring note, which applies identically here.
func CppResolver() MarkerResolver {
	return MarkerResolver{
		Language:   "cpp",
		Extensions: []string{".cc", ".cpp", ".cxx", ".c++", ".hh", ".hpp", ".hxx", ".h++"},
		Markers:    cMarkers,
	}
}

// BashResolver resolves shell script files (.sh, .bash) by walking up for
// the nearest .git directory. Unlike every other MarkerResolver preset in
// this file, bash has no analysis-scoping config file of its own to use as
// a marker: verified against bash-language-server's own documentation and
// its CLI source (github.com/bash-lsp/bash-language-server) — its workspace
// configuration travels over LSP (workspace/configuration) and a
// BASH_IDE_LOG_LEVEL environment variable, not a root-marker file the way
// tsconfig.json / pyrightconfig.json / Cargo.toml / pubspec.yaml scope
// those other resolvers.
//
// Rather than invent a marker bash-language-server does not itself
// recognize, this resolver uses the nearest .git directory as a pragmatic,
// honest fallback (the closest thing to a universal project-root signal a
// shell script has) — os.Stat matches it whether it is a directory (an
// ordinary clone) or a plain file (a git worktree's ".git" pointer file),
// so FindUp finds it either way. Like every marker resolver except
// AngularResolver, it still falls back to the script's own directory when
// no .git is found at all, so a single standalone script is never left
// unclaimed. See [CResolver]'s doc comment for the DefaultChain wiring
// note, which applies identically here.
func BashResolver() MarkerResolver {
	return MarkerResolver{
		Language:   "shellscript",
		Extensions: []string{".sh", ".bash"},
		Markers:    []string{".git"},
	}
}

// DefaultChain returns the resolver chain for every language lspbridge
// supports out of the box, tried in this order: Python, TypeScript,
// JavaScript, Rust, Dart, Angular. It is the broker's default
// Config.Resolver. Each resolver claims a disjoint Extensions set, so no
// file can match more than one — the order only affects readability, not
// correctness. AngularResolver is the one exception to "extension implies
// claim": its RequireMarker means a bare .html file with no angular.json
// above it is not claimed by anything in this chain (there is no
// plain-HTML language server preset), which is the intended behavior, not
// a gap.
//
// CResolver, CppResolver, and BashResolver (above) are NOT included here —
// see CResolver's doc comment for why that wiring is out of scope for this
// set of additions.
func DefaultChain() Chain {
	return Chain{
		PythonResolver(),
		TypeScriptResolver(),
		JavaScriptResolver(),
		RustResolver(),
		DartResolver(),
		AngularResolver(),
		CResolver(),
		CppResolver(),
		BashResolver(),
	}
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
