package server

import "os/exec"

// BashLanguageServerCommand is the bash-language-server binary name
// resolved via PATH. The npm package is also named "bash-language-server"
// (github.com/bash-lsp/bash-language-server); its binary name matches the
// package name exactly, the same shape as [RustAnalyzerCommand] /
// [TypeScriptLanguageServerCommand].
const BashLanguageServerCommand = "bash-language-server"

// BashLanguageServerSpec returns the [Spec] to launch bash-language-server
// over stdio, rooted at dir (the shell-script project root discovered by
// projectcontext.BashResolver — see its doc comment for why that resolver's
// marker is a pragmatic .git fallback rather than a bash-specific config
// file).
//
// Args is exactly ["start"]. Verified directly against the project's own
// CLI source (server/src/cli.ts, github.com/bash-lsp/bash-language-server,
// main branch, fetched 2026-09-13) — its printHelp() usage string reads:
//
//	Usage:
//	  bash-language-server start          Start listening on stdin/stdout
//	  bash-language-server -h, --help     Display this help and exit
//	  bash-language-server -v, --version  Print the version and exit
//
// So "start" is a subcommand, not a flag, and it is the only one that
// launches the server; there is no separate "--stdio" flag to pass (unlike
// [PyrightSpec] / [TypeScriptLanguageServerSpec] / [AngularLanguageServerSpec])
// because "start" always means stdin/stdout — bash-language-server has no
// other transport, the same single-mode shape as [RustAnalyzerSpec].
//
// bash-language-server is NOT installed on the development host this Spec
// was written on, so — unlike [ClangdSpec] — this Spec has never been
// exercised against a real running server; see this package's
// bashls_test.go for how the "found" code path is still proven
// deterministically via a fake stand-in binary, the same technique
// [AngularLanguageServerSpec]'s tests use for the same reason.
func BashLanguageServerSpec(dir string) (Spec, error) {
	path, err := exec.LookPath(BashLanguageServerCommand)
	if err != nil {
		return Spec{}, missingBinaryError(BashLanguageServerCommand, BashLanguageServerCommand, err)
	}
	return Spec{
		Command: path,
		Args:    []string{"start"},
		Dir:     dir,
	}, nil
}
