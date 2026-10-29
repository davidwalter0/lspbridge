package gts

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/davidwalter0/gotreesitter"
	"github.com/davidwalter0/lspbridge/gtslsp/validate"
	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/lspbridge/pkg/lspserver"
)

// Version identifies the server in initialize.serverInfo. definition,
// references and completion are name-matching heuristics, and the version
// string says so because clients show it.
const Version = "0.1.0 (definition/references/completion are HEURISTIC name matching)"

const (
	validatorDebounce = 300 * time.Millisecond
	validatorTimeout  = 20 * time.Second
	maxSyntaxDiags    = 200
)

// Config configures a Server.
type Config struct {
	Hooks      Hooks
	Validators []validate.Validator
}

// Server is the gts-lsp feature layer: per-document trees plus the request
// handlers, wired onto lspserver.
type Server struct {
	cfg Config

	mu     sync.Mutex
	states map[lsp.DocumentURI]*docState
	warned map[string]bool // validators already announced unavailable
	noLang sync.Mutex      // guards docState fields of documents with no grammar
}

// mu is the mutex that guards st's mutable fields: its language's, or the
// server-wide one for a document no grammar claims.
func (s *Server) lockOf(st *docState) *sync.Mutex {
	if st.lang != nil {
		return &st.lang.mu
	}
	return &s.noLang
}

// docState is the parse state of one open document. All fields except the
// immutable ones are guarded by lang.mu.
type docState struct {
	uri  lsp.DocumentURI
	lang *Lang // nil: no grammar claims this document

	doc  *lspserver.Doc // latest snapshot
	view []byte         // bytes the tree was parsed from (== doc.Text unless Hooks.ParseView)
	tree *gotreesitter.Tree

	stopped  bool   // last parse hit a timeout/limit
	parseErr string // last parse failed outright

	tags       []gotreesitter.Tag
	tagsFor    int // doc version tags were computed for; -1 none
	syntaxDiag []lsp.Diagnostic

	// external-validator state, guarded by Server.mu
	ext   map[string][]lsp.Diagnostic
	timer *time.Timer
}

// New creates the feature layer.
func New(cfg Config) *Server {
	return &Server{cfg: cfg, states: map[lsp.DocumentURI]*docState{}, warned: map[string]bool{}}
}

// Options returns the lspserver options that run this feature layer.
func (s *Server) Options() lspserver.Options {
	return lspserver.Options{
		Info:         &lsp.ServerInfo{Name: "gts-lsp", Version: Version},
		Capabilities: serverCapabilities(),
		Handler:      jsonrpc.HandlerFunc(s.handle),
		OnOpen:       s.onOpen,
		OnChange:     s.onChange,
		OnSave:       s.onSave,
		OnClose:      s.onClose,
	}
}

// serverCapabilities is the UNION over all languages; a request for a
// document whose grammar lacks the query returns an empty result, never an
// error.
func serverCapabilities() map[string]any {
	return map[string]any{
		"documentSymbolProvider": true,
		"foldingRangeProvider":   true,
		"selectionRangeProvider": true,
		"definitionProvider":     true,             // HEURISTIC
		"referencesProvider":     true,             // HEURISTIC
		"completionProvider":     map[string]any{}, // HEURISTIC
		"semanticTokensProvider": map[string]any{
			"legend": Legend(),
			"full":   true,
		},
	}
}

func (s *Server) state(uri lsp.DocumentURI) *docState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[uri]
}

func (s *Server) onOpen(ctx context.Context, srv *lspserver.Server, d *lspserver.Doc) {
	st := &docState{uri: d.URI, doc: d, tagsFor: -1, ext: map[string][]lsp.Diagnostic{}}
	st.lang = s.cfg.Hooks.Detect(d.Path, d.LanguageID, d.Text)
	s.mu.Lock()
	s.states[d.URI] = st
	s.mu.Unlock()
	s.reparse(st, d, nil)
	s.publish(ctx, srv, st)
	s.scheduleValidators(ctx, srv, st, true)
}

func (s *Server) onChange(ctx context.Context, srv *lspserver.Server, c *lspserver.Change) {
	st := s.state(c.New.URI)
	if st == nil {
		return
	}
	s.reparse(st, c.New, c)
	s.publish(ctx, srv, st)
}

func (s *Server) onSave(ctx context.Context, srv *lspserver.Server, d *lspserver.Doc) {
	if st := s.state(d.URI); st != nil {
		s.scheduleValidators(ctx, srv, st, false)
	}
}

func (s *Server) onClose(ctx context.Context, srv *lspserver.Server, d *lspserver.Doc) {
	s.mu.Lock()
	st := s.states[d.URI]
	delete(s.states, d.URI)
	if st != nil && st.timer != nil {
		st.timer.Stop()
	}
	s.mu.Unlock()
	if st != nil {
		_ = srv.PublishDiagnostics(ctx, d.URI, 0, nil) // clear stale squiggles
	}
}

// reparse brings st.tree up to date with doc. ch, when non-nil and ranged,
// lets the parse be incremental.
func (s *Server) reparse(st *docState, doc *lspserver.Doc, ch *lspserver.Change) {
	l := st.lang
	if l == nil {
		s.noLang.Lock()
		st.doc = doc
		s.noLang.Unlock()
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	st.doc = doc
	view := doc.Text
	if v := s.cfg.Hooks.ParseView; v != nil {
		if nv := v(doc.Path, l.Name, doc.Text); len(nv) == len(doc.Text) {
			view = nv
		}
	}
	var old *gotreesitter.Tree
	if ch != nil && !ch.Full && len(ch.Edits) > 0 && st.tree != nil && len(st.view) == len(ch.Old.Text) {
		old = st.tree
		for _, e := range ch.Edits {
			old.Edit(gotreesitter.InputEdit{
				StartByte: uint32(e.StartByte), OldEndByte: uint32(e.OldEndByte), NewEndByte: uint32(e.NewEndByte),
				StartPoint:  gotreesitter.Point{Row: uint32(e.StartPoint.Row), Column: uint32(e.StartPoint.Col)},
				OldEndPoint: gotreesitter.Point{Row: uint32(e.OldEndPoint.Row), Column: uint32(e.OldEndPoint.Col)},
				NewEndPoint: gotreesitter.Point{Row: uint32(e.NewEndPoint.Row), Column: uint32(e.NewEndPoint.Col)},
			})
		}
	}
	tree, stopped, err := l.parse(view, old)
	// An incremental parse is trusted only when it comes out clean. Error
	// recovery is not canonical under reuse: measured on the C grammar, an
	// incremental reparse after repairing a broken line kept the old ERROR
	// node (wrong diagnostic) where a fresh parse of the same text is clean.
	// So a failed or error-bearing incremental result is redone from scratch.
	if old != nil && (err != nil || tree == nil || stopped || tree.RootNode() == nil || tree.RootNode().HasErrorOrMissing()) {
		tree, stopped, err = l.parse(view, nil)
	}
	st.view = view
	st.tags, st.tagsFor = nil, -1
	st.stopped = stopped
	st.parseErr = ""
	if err != nil || tree == nil {
		st.tree = nil
		if err != nil {
			st.parseErr = err.Error()
		}
	} else {
		// The previous tree is deliberately NOT Released: a request running
		// on another goroutine cannot hold it (we hold l.mu), but the new
		// tree may share its nodes, and Release recycles their arenas.
		st.tree = tree
	}
	st.syntaxDiag = s.syntaxDiagnostics(st)
}

// publish sends syntax diagnostics merged with the external validators'.
func (s *Server) publish(ctx context.Context, srv *lspserver.Server, st *docState) {
	var syntax []lsp.Diagnostic
	m := s.lockOf(st)
	m.Lock()
	syntax = append(syntax, st.syntaxDiag...)
	version := st.doc.Version
	m.Unlock()
	s.mu.Lock()
	all := syntax
	names := make([]string, 0, len(st.ext))
	for n := range st.ext {
		names = append(names, n)
	}
	sortStrings(names)
	for _, n := range names {
		all = append(all, st.ext[n]...)
	}
	s.mu.Unlock()
	_ = srv.PublishDiagnostics(ctx, st.uri, version, all)
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// scheduleValidators debounces the external validators for st. On open the
// unavailable ones are announced once per session.
func (s *Server) scheduleValidators(ctx context.Context, srv *lspserver.Server, st *docState, opened bool) {
	if len(s.cfg.Validators) == 0 || st.lang == nil && !s.anyApplies(st) {
		return
	}
	s.mu.Lock()
	if st.timer != nil {
		st.timer.Stop()
	}
	st.timer = time.AfterFunc(validatorDebounce, func() { s.runValidators(ctx, srv, st) })
	s.mu.Unlock()
}

func (s *Server) vdoc(st *docState) validate.Document {
	name := ""
	if st.lang != nil {
		name = st.lang.Name
	}
	return validate.Document{Path: st.doc.Path, Language: name, Text: st.doc.Text}
}

func (s *Server) anyApplies(st *docState) bool {
	m := s.lockOf(st)
	m.Lock()
	d := s.vdoc(st)
	m.Unlock()
	for _, v := range s.cfg.Validators {
		if v.Applies(d) {
			return true
		}
	}
	return false
}

func (s *Server) runValidators(ctx context.Context, srv *lspserver.Server, st *docState) {
	if ctx.Err() != nil || s.state(st.uri) != st {
		return
	}
	// Snapshot the document once, so every validator sees the same text.
	m := s.lockOf(st)
	m.Lock()
	doc := s.vdoc(st)
	m.Unlock()
	var wg sync.WaitGroup
	for _, v := range s.cfg.Validators {
		if !v.Applies(doc) {
			continue
		}
		if ok, hint := v.Available(); !ok {
			s.mu.Lock()
			first := !s.warned[v.Name()]
			s.warned[v.Name()] = true
			delete(st.ext, v.Name())
			s.mu.Unlock()
			if first {
				_ = srv.ShowMessage(ctx, lsp.MessageTypeWarning, v.Name()+" is not installed, so its diagnostics are off. "+hint)
			}
			continue
		}
		wg.Add(1)
		go func(v validate.Validator) {
			defer wg.Done()
			vctx, cancel := context.WithTimeout(ctx, validatorTimeout)
			defer cancel()
			diags, err := v.Validate(vctx, doc)
			s.mu.Lock()
			if err != nil {
				diags = []lsp.Diagnostic{{
					Range:    lsp.Range{},
					Severity: lsp.SeverityWarning,
					Source:   v.Name(),
					Message:  "validator failed: " + err.Error(),
				}}
			}
			st.ext[v.Name()] = diags
			s.mu.Unlock()
		}(v)
	}
	wg.Wait()
	if s.state(st.uri) == st {
		s.publish(ctx, srv, st)
	}
}

func (s *Server) handle(ctx context.Context, req *jsonrpc.Request) (any, error) {
	switch req.Method {
	case "textDocument/documentSymbol":
		var p lsp.DocumentSymbolParams
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		return s.documentSymbols(p.TextDocument.URI), nil
	case "textDocument/foldingRange":
		var p lsp.FoldingRangeParams
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		return s.foldingRanges(p.TextDocument.URI), nil
	case "textDocument/selectionRange":
		var p lsp.SelectionRangeParams
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		return s.selectionRanges(p.TextDocument.URI, p.Positions), nil
	case "textDocument/semanticTokens/full":
		var p lsp.SemanticTokensParams
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		return s.semanticTokens(p.TextDocument.URI), nil
	case "textDocument/definition":
		var p lsp.DefinitionParams
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		return s.definition(p.TextDocument.URI, p.Position), nil
	case "textDocument/references":
		var p lsp.ReferenceParams
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		return s.references(p.TextDocument.URI, p.Position, p.Context.IncludeDeclaration), nil
	case "textDocument/completion":
		var p lsp.CompletionParams
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		return s.completion(p.TextDocument.URI, p.Position), nil
	}
	if req.ID != nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "method not found: " + req.Method}
	}
	return nil, nil
}

func decode(req *jsonrpc.Request, v any) error {
	if err := json.Unmarshal(req.Params, v); err != nil {
		return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: err.Error()}
	}
	return nil
}
