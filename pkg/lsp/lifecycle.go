package lsp

import "encoding/json"

// InitializeParams is the subset of LSP initialize parameters the bridge
// sends. It is deliberately sparse for the P0 skeleton; capabilities are
// extended per server as features are wired in.
type InitializeParams struct {
	ProcessID    int                `json:"processId"`
	RootURI      DocumentURI        `json:"rootUri"`
	Capabilities ClientCapabilities `json:"capabilities"`
	ClientInfo   *ClientInfo        `json:"clientInfo,omitempty"`
}

// ClientInfo identifies the client to the server.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ClientCapabilities is the advertised client capability set. Kept minimal
// and honest for P0; grown as concrete features (rename, references,
// diagnostics pull) are added.
type ClientCapabilities struct {
	Workspace    *WorkspaceClientCapabilities    `json:"workspace,omitempty"`
	TextDocument *TextDocumentClientCapabilities `json:"textDocument,omitempty"`
}

// WorkspaceClientCapabilities is the workspace-scoped capability subset.
type WorkspaceClientCapabilities struct {
	WorkspaceEdit *WorkspaceEditClientCapabilities `json:"workspaceEdit,omitempty"`
}

// WorkspaceEditClientCapabilities advertises WorkspaceEdit support.
type WorkspaceEditClientCapabilities struct {
	DocumentChanges bool `json:"documentChanges,omitempty"`
}

// TextDocumentClientCapabilities is the document-scoped capability subset.
type TextDocumentClientCapabilities struct {
	// PublishDiagnostics advertises support for the server-to-client
	// textDocument/publishDiagnostics notification. Per-feature capability
	// structs continue to land here as they're wired in.
	PublishDiagnostics *PublishDiagnosticsClientCapabilities `json:"publishDiagnostics,omitempty"`
	// DocumentSymbol advertises textDocument/documentSymbol support; its
	// HierarchicalDocumentSymbolSupport flag selects the nested DocumentSymbol
	// result shape over the flat SymbolInformation one.
	DocumentSymbol *DocumentSymbolClientCapabilities `json:"documentSymbol,omitempty"`
}

// DocumentSymbolClientCapabilities advertises the client's
// textDocument/documentSymbol support.
type DocumentSymbolClientCapabilities struct {
	// HierarchicalDocumentSymbolSupport asks the server to return the nested
	// [DocumentSymbol] tree rather than a flat [SymbolInformation] list.
	HierarchicalDocumentSymbolSupport bool `json:"hierarchicalDocumentSymbolSupport,omitempty"`
}

// PublishDiagnosticsClientCapabilities advertises the client's
// textDocument/publishDiagnostics support.
type PublishDiagnosticsClientCapabilities struct {
	RelatedInformation bool `json:"relatedInformation,omitempty"`
}

// InitializeResult is the server's initialize response. The raw capabilities
// object is retained verbatim so per-server feature detection can inspect it
// without this package modeling the whole surface.
type InitializeResult struct {
	Capabilities json.RawMessage `json:"capabilities"`
	ServerInfo   *ServerInfo     `json:"serverInfo,omitempty"`
}

// ServerInfo identifies the server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// TextDocumentItem is a document opened on the server.
type TextDocumentItem struct {
	URI        DocumentURI `json:"uri"`
	LanguageID string      `json:"languageId"`
	Version    int         `json:"version"`
	Text       string      `json:"text"`
}

// DidOpenTextDocumentParams is the textDocument/didOpen payload.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// VersionedTextDocumentIdentifier identifies a document at a version.
type VersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version int         `json:"version"`
}

// TextDocumentContentChangeEvent is a full-document content replacement
// (whole-document sync; the incremental form is added when a server needs it).
type TextDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

// DidChangeTextDocumentParams is the textDocument/didChange payload.
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}
