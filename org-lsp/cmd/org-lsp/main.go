// Command org-lsp is a pure-Go, Emacs-free Language Server Protocol server
// for org-mode text documents. It speaks LSP over stdio, in the same shape
// as e.g. `pyright-langserver --stdio` — point any LSP-capable editor
// (VS Code, Neovim, Zed, Emacs's own eglot/lsp-mode) at it for org-mode
// buffers, no running Emacs required.
//
// v1 surface: textDocument/documentSymbol, textDocument/foldingRange,
// workspace/symbol, and a small "org-lint-lite" diagnostics pass (broken
// file: links, unclosed #+begin_ blocks). See
// [github.com/davidwalter0/lspbridge/org-lsp/pkg/orglsp.Server] for the
// exact advertised capabilities.
//
// # Non-goals (strata boundary)
//
// No babel execution, no agenda, no table-formula evaluation. Those are org
// BEHAVIOR, not org SYNTAX — elisp is the spec for behavior, per
// orgmode.org/worg/org-syntax.html's own scope (it documents syntax, not
// behavior) — so they are deliberately out of scope here and stay real
// Emacs's job (`emacs --batch -l org` / emacsclient), not something this
// server tries to approximate. See the org-lsp/README.org for the full
// rationale and the mgmt 6131a2a1 task this implements.
package main

import (
	"context"
	"os"

	"github.com/davidwalter0/lspbridge/org-lsp/pkg/orglsp"
	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
)

func main() {
	os.Exit(run())
}

// run wires the server to stdio and blocks until the client disconnects or
// sends "exit", returning the process exit code the LSP spec prescribes:
// 0 if "shutdown" was received before "exit" (or the connection simply
// closed normally), 1 otherwise.
func run() int {
	srv := orglsp.NewServer()
	exitc := make(chan bool, 1) // value: whether shutdown preceded exit
	srv.SetOnExit(func(shutdownFirst bool) { exitc <- shutdownFirst })

	conn := jsonrpc.NewConn(jsonrpc.Join(os.Stdin, os.Stdout), srv)
	srv.SetNotifier(func(ctx context.Context, method string, params any) {
		_ = conn.Notify(ctx, method, params)
	})

	clean := true
	select {
	case clean = <-exitc:
	case <-conn.Done():
		clean = false
	}
	_ = conn.Close()
	if !clean {
		return 1
	}
	return 0
}
