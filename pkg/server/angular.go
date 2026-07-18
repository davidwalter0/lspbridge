package server

import "os/exec"

// AngularLanguageServerCommand is the Angular Language Server's binary name
// resolved via PATH. The npm package is "@angular/language-server", but —
// unlike typescript-language-server / rust-analyzer, whose binary name
// matches their package name exactly — it installs a bin shim named
// "ngserver" (mirrors [PyrightCommand]'s "pyright-langserver" differing
// from the bare "pyright" package name). Verified against a real (if
// otherwise incomplete) `npm install -g @angular/language-server` on the
// development host: `~/.npm-global/bin/ngserver` is a symlink to
// `../lib/node_modules/@angular/language-server/bin/ngserver`, a one-line
// `#!/usr/bin/env node` shim requiring the package's index.js.
const AngularLanguageServerCommand = "ngserver"

// AngularLanguageServerSpec returns the [Spec] to launch the Angular
// Language Server over stdio, rooted at dir (the Angular CLI workspace
// root discovered by projectcontext.AngularResolver, i.e. the nearest
// angular.json).
//
// Args, in order:
//   - "--stdio": selects the stdin/stdout transport. This is NOT the
//     server's default — verified directly against the installed
//     @angular/language-server v20.0.1's own `--help` usage text: "--stdio:
//     Communicate over stdin/stdout." is listed under "Additional options
//     supported by vscode-languageserver" alongside "--node-ipc:
//     Communicate using Node's IPC. This is the default." Omitting --stdio
//     would leave the server expecting a Node IPC channel this Spec never
//     sets up.
//   - "--tsProbeLocations" / "--ngProbeLocations": both required by the
//     server (its own usage text: "Path of typescript. Required." /
//     "Path of @angular/language-service. Required."). Both are set to dir
//     itself, not dir's node_modules — the server resolves each package via
//     Node's own `require.resolve(pkg, {paths: [probeLocation]})`
//     algorithm (confirmed by reading the installed server's bundled
//     resolver source), which treats a probe location as a *starting
//     point* and walks its own "<location>/node_modules/<pkg>" then up
//     through parents — exactly the standard layout of an Angular CLI
//     project, where `dir` is the workspace root and `dir/node_modules`
//     holds both `typescript` and `@angular/language-service` after
//     `npm install`. This is also what community LSP clients pass (e.g.
//     the neovim config surveyed via web search uses
//     `--ngProbeLocations ./` — the project root — not a node_modules
//     subpath).
//
// This Spec was NOT exercised live: @angular/language-server is not
// available in a working state on the development host (see the doc
// comment on projectcontext.AngularResolver and the session report for
// what was actually found and probed there). Args were verified by reading
// the installed (if incomplete) v20.0.1 package's own bundled source and
// its `--help` usage text directly, cross-checked against Angular's
// official docs (https://angular.dev/tools/language-service) and the
// lsp-mode Angular client
// (https://emacs-lsp.github.io/lsp-mode/page/lsp-angular/) — not run
// end-to-end. See [TypeScriptLanguageServerSpec]'s doc comment for the
// missing-binary error and zero-dependency-invariant convention this Spec
// shares; see [missingBinaryError]'s doc comment for why this Spec's
// remediation message names "angular-language-server" (exttool's
// package-derived tool name) rather than "ngserver" (the resolved binary).
func AngularLanguageServerSpec(dir string) (Spec, error) {
	path, err := exec.LookPath(AngularLanguageServerCommand)
	if err != nil {
		return Spec{}, missingBinaryError(AngularLanguageServerCommand, "angular-language-server", err)
	}
	return Spec{
		Command: path,
		Args:    []string{"--stdio", "--tsProbeLocations", dir, "--ngProbeLocations", dir},
		Dir:     dir,
	}, nil
}
