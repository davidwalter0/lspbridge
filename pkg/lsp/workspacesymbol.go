package lsp

import (
	"context"
	"encoding/json"
)

// workspaceSymbolMethod is the workspace/symbol method name.
const workspaceSymbolMethod = "workspace/symbol"

// WorkspaceSymbolParams is the workspace/symbol request payload. Per the LSP
// spec, Query should be matched "in a relaxed way" (case-insensitive,
// characters appearing in order); an empty Query requests every symbol.
type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

// WorkspaceSymbol requests symbols matching params.Query across the whole
// workspace. The result is the flat [SymbolInformation] shape; this package
// does not model the newer, lazily-resolved WorkspaceSymbol[] result (LSP
// 3.17+) since no server built on lspbridge emits it yet. A null result
// yields a nil slice and a nil error.
func (c *Client) WorkspaceSymbol(ctx context.Context, params WorkspaceSymbolParams) ([]SymbolInformation, error) {
	var raw json.RawMessage
	if err := c.conn.Call(ctx, workspaceSymbolMethod, params, &raw); err != nil {
		return nil, err
	}
	if isJSONNull(raw) {
		return nil, nil
	}
	var syms []SymbolInformation
	if err := json.Unmarshal(raw, &syms); err != nil {
		return nil, err
	}
	return syms, nil
}
