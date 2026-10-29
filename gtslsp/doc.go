// Package gtslsp is a tree-sitter-backed language server: one process that
// answers the syntactic half of LSP (diagnostics for parse errors, document
// symbols, folding, selection ranges, semantic tokens) for every grammar
// gotreesitter ships, plus name-based definition/references that are labelled
// heuristic. It lives in its own module so the lspbridge root module stays
// zero-dependency and CGO-free.
package gtslsp
