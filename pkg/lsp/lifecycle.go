package lsp

// InitializeParams is the subset of LSP initialize parameters the bridge
// sends. It is deliberately sparse for the P0 skeleton; capabilities are
// extended per server as features are wired in.
//
// InitializeParams is hand-written rather than generated
// (cmd/lspgen/config.go): the real metaModel type composes _InitializeParams
// (~8 fields including deprecated rootPath, a nullable rootUri, locale,
// trace, initializationOptions) with WorkspaceFoldersInitializeParams, and
// this client isn't ready to take on that whole surface yet.
type InitializeParams struct {
	ProcessID    int                `json:"processId"`
	RootURI      DocumentURI        `json:"rootUri"`
	Capabilities ClientCapabilities `json:"capabilities"`
	ClientInfo   *ClientInfo        `json:"clientInfo,omitempty"`
}

// TextDocumentContentChangeEvent is a full-document content replacement
// (whole-document sync; the incremental form is added when a server needs it).
//
// TextDocumentContentChangeEvent is hand-written rather than generated
// (cmd/lspgen/config.go): the metaModel type is
// TextDocumentContentChangePartial | TextDocumentContentChangeWholeDocument,
// and lspbridge intentionally models only the whole-document arm.
type TextDocumentContentChangeEvent struct {
	Text string `json:"text"`
}
