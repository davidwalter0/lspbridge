package server

import "os/exec"

// TypeScriptLanguageServerCommand is the typescript-language-server binary
// name resolved via PATH. It is the reference "tsserver" wrapper for the
// whole JS/TS family: the same process serves TypeScript, TSX,
// JavaScript, and JSX documents — which grammar a given document uses is
// carried by the LSP languageId set at didOpen time (see
// projectcontext.TypeScriptResolver / JavaScriptResolver), not by anything
// in this Spec.
const TypeScriptLanguageServerCommand = "typescript-language-server"

// TypeScriptLanguageServerSpec returns the [Spec] to launch
// typescript-language-server over stdio, rooted at dir (the Node project
// root discovered by projectcontext.TypeScriptResolver / JavaScriptResolver).
// Like [PyrightSpec], it resolves the binary via exec.LookPath so callers get
// a clear "executable file not found in $PATH" error up front rather than a
// generic failure out of Launch.
//
// This Spec does not check the resolved binary's version: exttool
// (github.com/davidwalter0/exttool) is this ecosystem's declared installer
// and pin-tracker for non-Go LSP servers, but lspbridge does not import it —
// doing so would add a dependency and break this module's
// zero-external-dependency invariant (no go.sum; see README.org). In
// practice this means TypeScriptLanguageServerSpec runs whatever
// typescript-language-server it finds on PATH, old or new (e.g. a host with
// 4.3.4 installed still works even if exttool's manifest has since moved its
// pin to a newer release) — the missing-binary error below is the only
// exttool-aware behavior this package has room for without that dependency.
func TypeScriptLanguageServerSpec(dir string) (Spec, error) {
	path, err := exec.LookPath(TypeScriptLanguageServerCommand)
	if err != nil {
		return Spec{}, missingBinaryError(TypeScriptLanguageServerCommand, err)
	}
	return Spec{
		Command: path,
		Args:    []string{"--stdio"},
		Dir:     dir,
	}, nil
}
