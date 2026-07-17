package lsp

import (
	"context"

	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
)

// Client is a minimal LSP client lifecycle over a JSON-RPC connection. It
// owns the handshake and text-sync notifications the broker needs to keep a
// warm session; per-feature requests (references, rename, diagnostics) are
// layered on top as they are wired in.
type Client struct {
	conn *jsonrpc.Conn
}

// NewClient wraps an established [jsonrpc.Conn].
func NewClient(conn *jsonrpc.Conn) *Client { return &Client{conn: conn} }

// Initialize performs the LSP initialize request followed by the initialized
// notification, completing the handshake. The server's InitializeResult
// (including its raw capabilities) is returned.
func (c *Client) Initialize(ctx context.Context, params InitializeParams) (*InitializeResult, error) {
	var result InitializeResult
	if err := c.conn.Call(ctx, "initialize", params, &result); err != nil {
		return nil, err
	}
	if err := c.conn.Notify(ctx, "initialized", struct{}{}); err != nil {
		return nil, err
	}
	return &result, nil
}

// DidOpen notifies the server that a document was opened.
func (c *Client) DidOpen(ctx context.Context, params DidOpenTextDocumentParams) error {
	return c.conn.Notify(ctx, "textDocument/didOpen", params)
}

// DidChange notifies the server of a document change (whole-document sync).
func (c *Client) DidChange(ctx context.Context, params DidChangeTextDocumentParams) error {
	return c.conn.Notify(ctx, "textDocument/didChange", params)
}

// Shutdown requests an orderly shutdown and then sends exit. After Shutdown
// the underlying connection should be closed by the caller.
func (c *Client) Shutdown(ctx context.Context) error {
	if err := c.conn.Call(ctx, "shutdown", nil, nil); err != nil {
		return err
	}
	return c.conn.Notify(ctx, "exit", nil)
}
