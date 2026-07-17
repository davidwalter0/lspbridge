// Command fakelsp is a minimal LSP server used only by lspbridge's own tests
// to exercise the subprocess-launch path without depending on a real language
// server. It answers initialize and shutdown, and exits on the exit
// notification.
package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

func main() {
	done := make(chan struct{})
	handler := jsonrpc.HandlerFunc(func(_ context.Context, req *jsonrpc.Request) (any, error) {
		switch req.Method {
		case "initialize":
			return lsp.InitializeResult{
				Capabilities: json.RawMessage(`{"textDocumentSync":1}`),
				ServerInfo:   &lsp.ServerInfo{Name: "fakelsp", Version: "0.0.1"},
			}, nil
		case "exit":
			close(done)
			return nil, nil
		default: // shutdown, textDocument/*, etc. — acknowledged, no-op
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
