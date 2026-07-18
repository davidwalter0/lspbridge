package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
)

// Client is a thin broker-protocol client over a unix socket, for future
// mcp-ast (read) and ae (write) consumption. It is the CQRS multiplexing point
// on the caller side: each consumer builds its own LSP method + params and
// calls [Client.Query]; the broker keeps them on one warm session.
type Client struct {
	conn *jsonrpc.Conn
}

// Dial connects to the broker listening on socketPath.
func Dial(socketPath string) (*Client, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("broker: dial %s: %w", socketPath, err)
	}
	return &Client{conn: jsonrpc.NewConn(conn, nil)}, nil
}

// NewClient wraps an already-established [jsonrpc.Conn] (e.g. an in-memory pipe
// in tests, or a connection dialed by the caller).
func NewClient(conn *jsonrpc.Conn) *Client { return &Client{conn: conn} }

// Query forwards one LSP request through the broker and returns the raw LSP
// result bytes verbatim. The caller decodes them with the matching lsp result
// type (e.g. []lsp.Location for references).
func (c *Client) Query(ctx context.Context, req QueryRequest) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.conn.Call(ctx, MethodQuery, req, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// Status returns the broker's warm-session snapshot.
func (c *Client) Status(ctx context.Context) (*StatusResponse, error) {
	var resp StatusResponse
	if err := c.conn.Call(ctx, MethodStatus, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Shutdown asks the broker to stop. The daemon tears its sessions down and
// exits; the connection then closes from the far end.
func (c *Client) Shutdown(ctx context.Context) error {
	var resp ShutdownResponse
	return c.conn.Call(ctx, MethodShutdown, nil, &resp)
}

// Close closes the underlying connection.
func (c *Client) Close() error { return c.conn.Close() }
