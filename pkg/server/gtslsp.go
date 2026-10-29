package server

import "os/exec"

// GtsLspCommand is the gts-lsp binary name resolved via PATH. gts-lsp is this
// repository's own tree-sitter language server (nested module gtslsp/, built
// separately because it carries the gotreesitter dependency the root module
// must stay free of).
const GtsLspCommand = "gts-lsp"

// GtsLspSpec returns the [Spec] to launch gts-lsp over stdio, rooted at dir.
// gts-lsp speaks stdio by default, so Args is empty.
//
// gts-lsp is a syntactic server: diagnostics for parse errors, document
// symbols, folding, selection ranges and semantic tokens for every grammar
// gotreesitter ships, plus name-matching definition/references/completion that
// it labels HEURISTIC in its serverInfo. It is the broker's fallback for
// languages with no dedicated server preset; a dedicated server (pyright,
// clangd, ...) always wins where one is configured.
func GtsLspSpec(dir string) (Spec, error) {
	path, err := exec.LookPath(GtsLspCommand)
	if err != nil {
		return Spec{}, missingBinaryError(GtsLspCommand, GtsLspCommand, err)
	}
	return Spec{Command: path, Dir: dir}, nil
}
