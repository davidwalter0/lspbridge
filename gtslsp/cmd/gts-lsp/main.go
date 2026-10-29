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
//	                        (loads every grammar: seconds)
//	gts-lsp --languages     print JSON: the registered language names (instant)
//	gts-lsp --detect PATH   print the language that would claim PATH, exit 1 if none
//	gts-lsp --version
//	gts-lsp schemas sync [--k8s-version V] [--kinds a,b] [URL...]
//	                        fill the offline schema cache (the ONLY network use)
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
	if len(os.Args) > 1 && os.Args[1] == "schemas" {
		os.Exit(schemasCmd(os.Args[2:]))
	}
	caps := flag.Bool("capabilities", false, "print language -> supported features as JSON and exit")
	langs := flag.Bool("languages", false, "print the registered language names as JSON and exit")
	detect := flag.String("detect", "", "print the language that would claim `path` (exit 1 if none) and exit")
	version := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	registerExtraLanguages()

	switch {
	case *version:
		fmt.Println("gts-lsp", gts.Version)
		return
	case *langs:
		if err := json.NewEncoder(os.Stdout).Encode(gts.LanguageNames()); err != nil {
			fmt.Fprintln(os.Stderr, "gts-lsp:", err)
			os.Exit(1)
		}
		return
	case *detect != "":
		src, _ := os.ReadFile(*detect) // content sniff (.h as C++); absent file is fine
		if l := hooks().Detect(*detect, "", src); l != nil {
			fmt.Println(l.Name)
			return
		}
		os.Exit(1)
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
