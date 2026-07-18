package lsp

// types_gen.go is generated from the pinned LSP metaModel.json vendored at
// cmd/lspgen/metaModel.json. After bumping the pin (or editing
// cmd/lspgen/config.go's allowlist), regenerate with:
//
//	go generate ./pkg/lsp/...
//
// mgmt 8a6c4d87.

//go:generate go run github.com/davidwalter0/lspbridge/cmd/lspgen -out types_gen.go
