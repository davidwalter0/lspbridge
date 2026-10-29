// Command gts-lsp is a tree-sitter-backed language server over stdio. It
// answers the syntactic half of LSP for every grammar gotreesitter ships:
// parse-error diagnostics, document symbols, folding, selection ranges and
// semantic tokens, plus name-matching definition, references and completion
// that are HEURISTIC (no scopes, no types) and say so in serverInfo.
//
// It never offers hover, rename or type errors: a syntax tree cannot back them.
//
//	gts-lsp                 serve LSP on stdin/stdout
//	gts-lsp --capabilities  print JSON: language -> supported features
//	gts-lsp --version
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/davidwalter0/lspbridge/gtslsp/gts"
	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
	"github.com/davidwalter0/lspbridge/pkg/lspserver"
)

func main() {
	caps := flag.Bool("capabilities", false, "print language -> supported features as JSON and exit")
	version := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	registerExtraLanguages()

	switch {
	case *version:
		fmt.Println("gts-lsp", gts.Version)
		return
	case *caps:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(gts.Capabilities()); err != nil {
			fmt.Fprintln(os.Stderr, "gts-lsp:", err)
			os.Exit(1)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	srv := gts.New(gts.Config{Hooks: hooks(), Validators: validators()})
	os.Exit(lspserver.Serve(ctx, jsonrpc.Join(os.Stdin, os.Stdout), srv.Options()))
}
