package server

import (
	"os"
	"os/exec"
	"path/filepath"
)

// ClangdCommand is the clangd binary name resolved via PATH.
const ClangdCommand = "clangd"

// ClangdSpec returns the [Spec] to launch clangd over stdio for a C/C++
// file, rooted at dir (the project root discovered by
// projectcontext.CResolver / projectcontext.CppResolver, i.e. the nearest
// compile_commands.json, falling back to the source file's own directory
// when none is found).
//
// clangd speaks LSP over stdio unconditionally: verified directly against
// the installed clangd 14.0.6's own `--help` AND `--help-hidden` output on
// the development host — neither lists a `--stdio`, `--socket`, `--pipe`,
// nor `--tcp` flag, so (like [RustAnalyzerSpec]) there is no
// alternate-transport flag this Spec could omit or must pass; stdio is the
// only mode clangd has.
//
// "--compile-commands-dir="+dir is appended to Args ONLY when dir actually
// contains a compile_commands.json, checked here with the same os.Stat
// existence test [MarkerResolver.Resolve] uses to populate
// Context.ConfigPath — this is the "where present" half of this Spec's
// contract. clangd's own --help text documents the flag's fallback ("If
// path is invalid, clangd will look in the current directory and parent
// paths of each source file") only for an *invalid* (non-existent) path; it
// does not document what happens when a valid directory simply lacks the
// file, so this Spec never risks that unverified case — it omits the flag
// entirely when compile_commands.json is absent and relies on Dir (below)
// plus clangd's own default upward search, reproducing stock clangd
// behavior exactly rather than a guess.
//
// Dir is always set to dir, matching every other preset in this package —
// and it doubles as the anchor for clangd's own fallback search in the
// no-compile_commands.json branch above, since that search starts from
// "the current directory" of the running process.
func ClangdSpec(dir string) (Spec, error) {
	path, err := exec.LookPath(ClangdCommand)
	if err != nil {
		return Spec{}, missingBinaryError(ClangdCommand, ClangdCommand, err)
	}
	var args []string
	if _, statErr := os.Stat(filepath.Join(dir, "compile_commands.json")); statErr == nil {
		args = []string{"--compile-commands-dir=" + dir}
	}
	return Spec{
		Command: path,
		Args:    args,
		Dir:     dir,
	}, nil
}
