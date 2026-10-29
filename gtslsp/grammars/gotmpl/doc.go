// Package gotmpl registers a Go text/template grammar (helm chart templates)
// with gotreesitter's registry, out of tree. Register is the entry point the
// server calls once at startup.
//
// The grammar is ngalaiko/tree-sitter-go-template (MIT), vendored as
// grammar.json and compiled to gotmpl.bin; see README.md for provenance.
package gotmpl

//go:generate go run ./internal/gen -json grammar.json -bin gotmpl.bin
