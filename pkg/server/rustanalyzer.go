package server

import "os/exec"

// RustAnalyzerCommand is the rust-analyzer binary name resolved via PATH.
// Unlike pyright/typescript-language-server it takes no "--stdio" flag:
// speaking LSP over stdin/stdout is rust-analyzer's default (and only)
// behavior when invoked with no subcommand (verified against
// `rust-analyzer --help`; its subcommands are parser/debug utilities, not
// alternate transports).
const RustAnalyzerCommand = "rust-analyzer"

// RustAnalyzerSpec returns the [Spec] to launch rust-analyzer over stdio,
// rooted at dir (the crate/workspace root discovered by
// projectcontext.RustResolver, i.e. the nearest Cargo.toml). See
// [TypeScriptLanguageServerSpec]'s doc comment for the missing-binary error
// and zero-dependency-invariant convention this Spec shares.
func RustAnalyzerSpec(dir string) (Spec, error) {
	path, err := exec.LookPath(RustAnalyzerCommand)
	if err != nil {
		return Spec{}, missingBinaryError(RustAnalyzerCommand, err)
	}
	return Spec{
		Command: path,
		Dir:     dir,
	}, nil
}
