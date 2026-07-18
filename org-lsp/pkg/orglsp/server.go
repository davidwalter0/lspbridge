package orglsp

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// serverName/serverVersion identify org-lsp in its InitializeResult.
const (
	serverName    = "org-lsp"
	serverVersion = "0.1.0"
)

// capabilitiesJSON is org-lsp's advertised capability set. It names exactly
// what v1 implements and nothing else — no completion, hover, rename,
// definition, references, etc. — so a client never sends a request this
// server can only fail. textDocumentSync is Full (1): [Buffers] holds the
// whole document text; didChange always replaces it wholesale.
const capabilitiesJSON = `{` +
	`"textDocumentSync":1,` +
	`"documentSymbolProvider":true,` +
	`"foldingRangeProvider":true,` +
	`"workspaceSymbolProvider":true` +
	`}`

// Server is the org-lsp [jsonrpc.Handler]. It owns the open-buffer map and a
// per-root [WorkspaceIndex] (created once rootUri/rootPath is known, at
// initialize), and dispatches every method named in capabilitiesJSON plus
// the base lifecycle (initialize/didOpen/didChange/didClose/shutdown/exit).
// Zero value is not usable; construct with [NewServer].
type Server struct {
	buffers *Buffers

	mu    sync.Mutex
	index *WorkspaceIndex

	// notify sends a server-to-client notification (publishDiagnostics). It
	// is nil until [Server.SetNotifier] is called — the Handler and its
	// owning [jsonrpc.Conn] are mutually constructed (NewConn needs a
	// Handler; a notifying Handler needs the Conn), so this is wired after
	// the fact rather than in NewServer.
	notify func(ctx context.Context, method string, params any)

	// onExit, if set, is called when the "exit" notification arrives, with
	// whether "shutdown" was received first (the LSP spec's own success/
	// failure signal for the client's process-exit-code decision).
	onExit func(shutdownFirst bool)

	shutdownReceived bool
}

// NewServer returns a Server with no open buffers and no workspace root.
func NewServer() *Server {
	return &Server{buffers: NewBuffers()}
}

// SetNotifier installs the callback Server uses to send server-to-client
// notifications. Typically a wrapper around the owning [jsonrpc.Conn]'s
// Notify method.
func (s *Server) SetNotifier(fn func(ctx context.Context, method string, params any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notify = fn
}

// SetOnExit installs the callback Server invokes when it handles an "exit"
// notification, so the process hosting it (cmd/org-lsp) knows when — and
// how cleanly — to stop.
func (s *Server) SetOnExit(fn func(shutdownFirst bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onExit = fn
}

// Handle implements [jsonrpc.Handler].
func (s *Server) Handle(ctx context.Context, req *jsonrpc.Request) (any, error) {
	switch req.Method {
	case "initialize":
		return s.initialize(req.Params)
	case "initialized":
		return nil, nil
	case "textDocument/didOpen":
		return nil, s.didOpen(ctx, req.Params)
	case "textDocument/didChange":
		return nil, s.didChange(ctx, req.Params)
	case "textDocument/didClose":
		return nil, s.didClose(req.Params)
	case "textDocument/documentSymbol":
		return s.documentSymbol(req.Params)
	case "textDocument/foldingRange":
		return s.foldingRange(req.Params)
	case "workspace/symbol":
		return s.workspaceSymbol(req.Params)
	case "shutdown":
		s.mu.Lock()
		s.shutdownReceived = true
		s.mu.Unlock()
		return nil, nil
	case "exit":
		s.mu.Lock()
		clean := s.shutdownReceived
		onExit := s.onExit
		s.mu.Unlock()
		if onExit != nil {
			onExit(clean)
		}
		return nil, nil
	default:
		if req.ID == nil {
			return nil, nil // unknown notification: silently acknowledged
		}
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "method not found: " + req.Method}
	}
}

// initializeParams is the subset of the LSP initialize request org-lsp
// reads: just enough to resolve a workspace root for workspace/symbol.
type initializeParams struct {
	RootURI  lsp.DocumentURI `json:"rootUri"`
	RootPath string          `json:"rootPath"`
}

func (s *Server) initialize(raw json.RawMessage) (any, error) {
	var params initializeParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: err.Error()}
		}
	}
	root := params.RootPath
	if params.RootURI != "" {
		root = uriToPath(params.RootURI)
	}

	s.mu.Lock()
	if root != "" {
		s.index = NewWorkspaceIndex(root)
	}
	s.mu.Unlock()

	return lsp.InitializeResult{
		Capabilities: json.RawMessage(capabilitiesJSON),
		ServerInfo:   &lsp.ServerInfo{Name: serverName, Version: serverVersion},
	}, nil
}

func (s *Server) didOpen(ctx context.Context, raw json.RawMessage) error {
	var params lsp.DidOpenTextDocumentParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return err
	}
	s.buffers.Open(string(params.TextDocument.URI), params.TextDocument.Text, params.TextDocument.Version)
	s.publishDiagnostics(ctx, params.TextDocument.URI)
	return nil
}

func (s *Server) didChange(ctx context.Context, raw json.RawMessage) error {
	var params lsp.DidChangeTextDocumentParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return err
	}
	if len(params.ContentChanges) == 0 {
		return nil
	}
	// Whole-document sync (textDocumentSync=Full): exactly one entry,
	// carrying the full new text.
	text := params.ContentChanges[len(params.ContentChanges)-1].Text
	s.buffers.Change(string(params.TextDocument.URI), text, params.TextDocument.Version)
	s.publishDiagnostics(ctx, params.TextDocument.URI)
	return nil
}

func (s *Server) didClose(raw json.RawMessage) error {
	var params struct {
		TextDocument lsp.TextDocumentIdentifier `json:"textDocument"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return err
	}
	s.buffers.Close(string(params.TextDocument.URI))
	return nil
}

// publishDiagnostics runs org-lint-lite over uri's current buffer text and
// sends the result as a textDocument/publishDiagnostics notification. It is
// a no-op if no notifier is installed (e.g. in a unit test that exercises
// Handle directly) or the document isn't open.
func (s *Server) publishDiagnostics(ctx context.Context, uri lsp.DocumentURI) {
	s.mu.Lock()
	notify := s.notify
	s.mu.Unlock()
	if notify == nil {
		return
	}
	text, ok := s.buffers.Get(string(uri))
	if !ok {
		return
	}
	diags := Diagnostics(uriToPath(uri), text)
	if diags == nil {
		diags = []lsp.Diagnostic{} // explicit empty batch clears any stale report
	}
	notify(ctx, "textDocument/publishDiagnostics", lsp.PublishDiagnosticsParams{URI: uri, Diagnostics: diags})
}

func (s *Server) documentSymbol(raw json.RawMessage) (any, error) {
	var params lsp.DocumentSymbolParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: err.Error()}
	}
	text, ok := s.buffers.Get(string(params.TextDocument.URI))
	if !ok {
		return nil, nil
	}
	return DocumentSymbols(uriToPath(params.TextDocument.URI), text), nil
}

func (s *Server) foldingRange(raw json.RawMessage) (any, error) {
	var params lsp.FoldingRangeParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: err.Error()}
	}
	text, ok := s.buffers.Get(string(params.TextDocument.URI))
	if !ok {
		return nil, nil
	}
	return FoldingRanges(text), nil
}

func (s *Server) workspaceSymbol(raw json.RawMessage) (any, error) {
	var params lsp.WorkspaceSymbolParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: err.Error()}
	}
	s.mu.Lock()
	idx := s.index
	s.mu.Unlock()
	if idx == nil {
		return []lsp.SymbolInformation{}, nil
	}
	syms, err := idx.Symbols(params.Query)
	if err != nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: err.Error()}
	}
	return syms, nil
}
