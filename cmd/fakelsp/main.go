// Command fakelsp is a minimal LSP server used only by lspbridge's own tests
// to exercise the subprocess-launch and broker-forwarding paths without
// depending on a real language server. It answers initialize and shutdown,
// returns a canned textDocument/documentSymbol result (so the broker's
// forward-and-return path can be asserted deterministically), and exits on the
// exit notification.
package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// cannedDocumentSymbol is the fixed hierarchical documentSymbol reply the fake
// server returns, letting broker tests assert an exact forwarded result.
var cannedDocumentSymbol = []lsp.DocumentSymbol{{
	Name: "cannedSymbol",
	Kind: lsp.SymbolKindFunction,
	Range: lsp.Range{
		Start: lsp.Position{Line: 0, Character: 0},
		End:   lsp.Position{Line: 0, Character: 12},
	},
	SelectionRange: lsp.Range{
		Start: lsp.Position{Line: 0, Character: 4},
		End:   lsp.Position{Line: 0, Character: 12},
	},
}}

func main() {
	done := make(chan struct{})
	handler := jsonrpc.HandlerFunc(func(_ context.Context, req *jsonrpc.Request) (any, error) {
		switch req.Method {
		case "initialize":
			return lsp.InitializeResult{
				Capabilities: json.RawMessage(`{"textDocumentSync":1,"documentSymbolProvider":true}`),
				ServerInfo:   &lsp.ServerInfo{Name: "fakelsp", Version: "0.0.1"},
			}, nil
		case "textDocument/documentSymbol":
			return cannedDocumentSymbol, nil
		case "exit":
			close(done)
			return nil, nil
		default: // shutdown, other textDocument/*, etc. — acknowledged, no-op
			return nil, nil
		}
	})

	conn := jsonrpc.NewConn(jsonrpc.Join(os.Stdin, os.Stdout), handler)
	defer func() { _ = conn.Close() }()

	select {
	case <-done:
	case <-conn.Done():
	}
}
