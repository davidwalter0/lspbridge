package server

import "os/exec"

// DartCommand is the Dart SDK binary name resolved via PATH. The Dart
// Analysis Server is not a standalone executable; it is invoked as a
// subcommand of the SDK's "dart" tool: `dart language-server`.
const DartCommand = "dart"

// DartAnalysisServerSpec returns the [Spec] to launch the Dart Analysis
// Server over stdio in LSP mode, rooted at dir (the pub package root
// discovered by projectcontext.DartResolver, i.e. the nearest pubspec.yaml).
//
// --protocol=lsp is passed explicitly even though it is already the
// server's default (verified live against `dart language-server --help`
// on Dart SDK 3.12.2: "--protocol=<protocol> ... [lsp] (default) ...
// [analyzer]") — this Spec never relies on an upstream default holding
// across SDK versions, the same defensive-explicitness convention
// [TypeScriptLanguageServerSpec] follows for "--stdio". See
// [TypeScriptLanguageServerSpec]'s doc comment for the missing-binary
// error and zero-dependency-invariant convention this Spec shares.
func DartAnalysisServerSpec(dir string) (Spec, error) {
	path, err := exec.LookPath(DartCommand)
	if err != nil {
		return Spec{}, missingBinaryError(DartCommand, DartCommand, err)
	}
	return Spec{
		Command: path,
		Args:    []string{"language-server", "--protocol=lsp"},
		Dir:     dir,
	}, nil
}
