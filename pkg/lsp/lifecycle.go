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

// TextDocumentContentChangeEvent is one content change. With Range nil it
// replaces the whole document; with Range set it replaces that range (Position
// characters count in the negotiated encoding, UTF-16 by default). Both new
// fields are omitempty, so a whole-document change still serializes to exactly
// {"text": ...} for servers that only understand full sync.
//
// TextDocumentContentChangeEvent is hand-written rather than generated
// (cmd/lspgen/config.go): the metaModel type is
// TextDocumentContentChangePartial | TextDocumentContentChangeWholeDocument,
// an "or" the generator rejects.
type TextDocumentContentChangeEvent struct {
	Range       *Range  `json:"range,omitempty"`
	RangeLength *uint32 `json:"rangeLength,omitempty"` // deprecated by the spec; kept for old clients
	Text        string  `json:"text"`
}
