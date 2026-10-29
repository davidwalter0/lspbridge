package lspserver

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// Options configures a server. Every field is optional.
type Options struct {
	// Info is returned as initialize's serverInfo.
	Info *lsp.ServerInfo
	// Capabilities is marshaled as initialize's capabilities object. The
	// server fills in textDocumentSync (incremental, open/close, save) and
	// positionEncoding (utf-16) when the map does not already set them.
	Capabilities map[string]any
	// Handler serves every request the skeleton does not own (everything but
	// initialize / shutdown). It is only reached after initialize.
	Handler jsonrpc.Handler
	// OnInitialize runs inside the initialize request, before the response.
	OnInitialize func(ctx context.Context, s *Server, p lsp.InitializeParams) error
	// Document hooks run synchronously on the read loop, in wire order, after
	// the store has been updated. They must not block on a client reply.
	OnOpen   func(ctx context.Context, s *Server, d *Doc)
	OnChange func(ctx context.Context, s *Server, c *Change)
	OnSave   func(ctx context.Context, s *Server, d *Doc)
	// OnClose receives the last snapshot of the closed document.
	OnClose func(ctx context.Context, s *Server, d *Doc)
}

type state int

const (
	stateNew state = iota
	stateRunning
	stateShutdown
)

// Server is one LSP session over one connection.
type Server struct {
	opts Options
	docs *Store
	conn *jsonrpc.Conn

	mu       sync.Mutex
	st       state
	exitCode int
	exitCh   chan struct{}
	exitOnce sync.Once
}

// Docs is the open-document store.
func (s *Server) Docs() *Store { return s.docs }

// Notify sends a notification to the client.
func (s *Server) Notify(ctx context.Context, method string, params any) error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	return conn.Notify(ctx, method, params)
}

// PublishDiagnostics sends textDocument/publishDiagnostics. A nil slice is
// sent as [] (JSON null would be a protocol violation and some clients drop it).
func (s *Server) PublishDiagnostics(ctx context.Context, uri lsp.DocumentURI, version int, diags []lsp.Diagnostic) error {
	if diags == nil {
		diags = []lsp.Diagnostic{}
	}
	return s.Notify(ctx, "textDocument/publishDiagnostics",
		lsp.PublishDiagnosticsParams{URI: uri, Version: version, Diagnostics: diags})
}

// ShowMessage sends window/showMessage.
func (s *Server) ShowMessage(ctx context.Context, t lsp.MessageType, msg string) error {
	return s.Notify(ctx, "window/showMessage", lsp.ShowMessageParams{Type: t, Message: msg})
}

// Serve runs a server over rwc until the client sends exit or the connection
// ends, closes rwc, and returns the process exit code the LSP spec prescribes:
// 0 if shutdown was received before exit, otherwise 1.
func Serve(ctx context.Context, rwc io.ReadWriteCloser, opts Options) int {
	s := &Server{opts: opts, docs: NewStore(), exitCh: make(chan struct{})}
	// The read loop starts inside NewConn and may dispatch at once; handle
	// takes s.mu first, so holding it here publishes s.conn before any
	// handler (or hook calling Notify) can read it.
	s.mu.Lock()
	s.conn = jsonrpc.NewConn(rwc, jsonrpc.HandlerFunc(s.handle))
	s.mu.Unlock()
	defer func() { _ = s.conn.Close() }()
	select {
	case <-s.exitCh:
	case <-s.conn.Done():
		s.mu.Lock()
		s.exitCode = 1 // connection lost without exit
		s.mu.Unlock()
	case <-ctx.Done():
		s.mu.Lock()
		s.exitCode = 1
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitCode
}

func (s *Server) handle(ctx context.Context, req *jsonrpc.Request) (any, error) {
	s.mu.Lock()
	st := s.st
	s.mu.Unlock()

	isReq := req.ID != nil
	switch req.Method {
	case "exit":
		s.exitOnce.Do(func() {
			s.mu.Lock()
			if s.st == stateShutdown {
				s.exitCode = 0
			} else {
				s.exitCode = 1
			}
			s.mu.Unlock()
			close(s.exitCh)
		})
		return nil, nil
	case "initialize":
		if st != stateNew {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidRequest, Message: "initialize called twice"}
		}
		return s.initialize(ctx, req)
	case "initialized":
		return nil, nil
	}

	if st == stateNew {
		if isReq {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeServerNotInitialized, Message: "server not initialized"}
		}
		return nil, nil // notifications before initialize are dropped
	}
	if st == stateShutdown {
		if isReq && req.Method != "shutdown" {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidRequest, Message: "server is shutting down"}
		}
	}

	switch req.Method {
	case "shutdown":
		s.mu.Lock()
		s.st = stateShutdown
		s.mu.Unlock()
		return nil, nil
	case "textDocument/didOpen":
		s.didOpen(ctx, req)
		return nil, nil
	case "textDocument/didChange":
		s.didChange(ctx, req)
		return nil, nil
	case "textDocument/didSave":
		var p lsp.DidSaveTextDocumentParams
		if json.Unmarshal(req.Params, &p) == nil && s.opts.OnSave != nil {
			if d := s.docs.Get(p.TextDocument.URI); d != nil {
				s.opts.OnSave(ctx, s, d)
			}
		}
		return nil, nil
	case "textDocument/didClose":
		var p lsp.DidCloseTextDocumentParams
		if json.Unmarshal(req.Params, &p) == nil {
			if d := s.docs.Close(p.TextDocument.URI); d != nil && s.opts.OnClose != nil {
				s.opts.OnClose(ctx, s, d)
			}
		}
		return nil, nil
	}

	if s.opts.Handler == nil {
		if isReq {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "method not found: " + req.Method}
		}
		return nil, nil
	}
	return s.opts.Handler.Handle(ctx, req)
}

func (s *Server) initialize(ctx context.Context, req *jsonrpc.Request) (any, error) {
	var p lsp.InitializeParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: err.Error()}
		}
	}
	if s.opts.OnInitialize != nil {
		if err := s.opts.OnInitialize(ctx, s, p); err != nil {
			return nil, err
		}
	}
	caps := map[string]any{}
	for k, v := range s.opts.Capabilities {
		caps[k] = v
	}
	if _, ok := caps["textDocumentSync"]; !ok {
		caps["textDocumentSync"] = map[string]any{
			"openClose": true,
			"change":    int(lsp.TextDocumentSyncKindIncremental),
			"save":      map[string]any{"includeText": false},
		}
	}
	caps["positionEncoding"] = string(lsp.PositionEncodingKindUTF16)
	raw, err := json.Marshal(caps)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.st = stateRunning
	s.mu.Unlock()
	return lsp.InitializeResult{Capabilities: raw, ServerInfo: s.opts.Info}, nil
}

func (s *Server) didOpen(ctx context.Context, req *jsonrpc.Request) {
	var p lsp.DidOpenTextDocumentParams
	if json.Unmarshal(req.Params, &p) != nil || p.TextDocument.URI == "" {
		return
	}
	d := s.docs.Open(p.TextDocument)
	if s.opts.OnOpen != nil {
		s.opts.OnOpen(ctx, s, d)
	}
}

func (s *Server) didChange(ctx context.Context, req *jsonrpc.Request) {
	var p lsp.DidChangeTextDocumentParams
	if json.Unmarshal(req.Params, &p) != nil {
		return
	}
	ch := s.docs.Apply(p)
	if ch != nil && s.opts.OnChange != nil {
		s.opts.OnChange(ctx, s, ch)
	}
}
