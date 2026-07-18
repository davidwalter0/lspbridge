package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/lspbridge/pkg/projectcontext"
	"github.com/davidwalter0/lspbridge/pkg/server"
)

// ErrClosed is returned by broker operations after [Broker.ShutdownAll].
var ErrClosed = errors.New("broker: closed")

// Default lifecycle timings.
const (
	// DefaultIdleTimeout is how long a warm session may sit unused before the
	// reaper shuts it down. ADR-0013 §Gap 3: sessions are stateful and
	// long-lived, so the model is activate-on-connect → persist → idle-reap.
	DefaultIdleTimeout = 10 * time.Minute

	initTimeout     = 60 * time.Second // ceiling for a server's initialize handshake
	shutdownTimeout = 5 * time.Second  // ceiling for one session's orderly shutdown
	brokerVersion   = "0.1.0"
)

// SpecFunc maps a resolved project [projectcontext.Context] to the
// [server.Spec] that launches its language server.
type SpecFunc func(pc projectcontext.Context) (server.Spec, error)

// DefaultSpecFor maps a Context to a launch Spec for the languages lspbridge
// supports out of the box: python → pyright; typescript / typescriptreact /
// javascript / javascriptreact → typescript-language-server (one process
// serves the whole JS/TS family — see [server.TypeScriptLanguageServerSpec]);
// rust → rust-analyzer. Unknown languages are an error (the broker cannot
// activate a session it has no server for).
func DefaultSpecFor(pc projectcontext.Context) (server.Spec, error) {
	switch pc.Language {
	case "python":
		return server.PyrightSpec(pc.Root)
	case "typescript", "typescriptreact", "javascript", "javascriptreact":
		return server.TypeScriptLanguageServerSpec(pc.Root)
	case "rust":
		return server.RustAnalyzerSpec(pc.Root)
	default:
		return server.Spec{}, fmt.Errorf("broker: no language server configured for %q", pc.Language)
	}
}

// Config configures a [Broker]. The zero value is usable: every field has a
// sane default ([projectcontext.DefaultChain] resolver + [DefaultSpecFor],
// 10-minute idle timeout, real clock, discarded server stderr).
type Config struct {
	// Resolver maps a source file to its project Context. Default:
	// [projectcontext.DefaultChain] (python, typescript, javascript, rust).
	Resolver projectcontext.Resolver
	// SpecFor maps a Context to its launch Spec. Default: DefaultSpecFor.
	SpecFor SpecFunc
	// IdleTimeout is the quiescence window before a session is reaped.
	// Default: DefaultIdleTimeout.
	IdleTimeout time.Duration
	// ServerStderr, if non-nil, receives every spawned server's stderr; nil
	// discards it.
	ServerStderr io.Writer
	// Clock, if non-nil, supplies the current time (injected in tests). Default:
	// time.Now.
	Clock func() time.Time
}

// Broker owns the warm-session cache and is safe for concurrent use.
type Broker struct {
	resolver    projectcontext.Resolver
	specFor     SpecFunc
	idleTimeout time.Duration
	stderr      io.Writer
	now         func() time.Time

	// baseCtx is the broker's lifetime context; servers are launched under it
	// so a single request's cancellation never reaps the warm subprocess
	// (exec.CommandContext binds process lifetime to the context it was started
	// with). cancel tears every server down on ShutdownAll.
	baseCtx context.Context
	cancel  context.CancelFunc

	mu       sync.Mutex
	sessions map[string]*session
	closed   bool
}

// session is one warm language-server session keyed by Context.Key.
type session struct {
	pc        projectcontext.Context
	collector *lsp.DiagnosticsCollector

	// ready is closed once the cold start settles (success or failure). It is
	// the single-flight barrier: concurrent ensureSession callers for the same
	// key find the placeholder and block here rather than spawning a second
	// server. srv/initErr are published before ready closes.
	ready   chan struct{}
	srv     *server.Server
	initErr error

	mu       sync.Mutex
	opened   map[lsp.DocumentURI]int // doc URI → last version synced
	lastUsed time.Time
	inflight int
}

// New returns a Broker configured by cfg (all fields optional).
func New(cfg Config) *Broker {
	b := &Broker{
		resolver:    cfg.Resolver,
		specFor:     cfg.SpecFor,
		idleTimeout: cfg.IdleTimeout,
		stderr:      cfg.ServerStderr,
		now:         cfg.Clock,
		sessions:    make(map[string]*session),
	}
	if b.resolver == nil {
		b.resolver = projectcontext.DefaultChain()
	}
	if b.specFor == nil {
		b.specFor = DefaultSpecFor
	}
	if b.idleTimeout <= 0 {
		b.idleTimeout = DefaultIdleTimeout
	}
	if b.now == nil {
		b.now = time.Now
	}
	b.baseCtx, b.cancel = context.WithCancel(context.Background())
	return b
}

// Query runs one LSP request against the warm session for req.File, spawning
// and initializing the session on first use. It returns the raw LSP result
// bytes verbatim (JSON "null" when the server had no result). The document's
// current content is synced (didOpen on first sight, didChange thereafter)
// before the method is forwarded, so a server answers against req.Text when the
// caller supplies unsaved content.
func (b *Broker) Query(ctx context.Context, req QueryRequest) (json.RawMessage, error) {
	if req.Method == "" {
		return nil, fmt.Errorf("broker: query requires method")
	}
	pc, err := b.resolveContext(req)
	if err != nil {
		return nil, err
	}
	s, err := b.ensureSession(pc)
	if err != nil {
		return nil, err
	}

	// Guard the session against idle-reap for the duration of this call.
	s.mu.Lock()
	s.inflight++
	s.lastUsed = b.now()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.inflight--
		s.lastUsed = b.now()
		s.mu.Unlock()
	}()

	text, err := b.contentFor(req)
	if err != nil {
		return nil, err
	}
	uri := lsp.DocumentURI("file://" + req.File)
	if err := s.syncDoc(ctx, uri, pc.Language, text); err != nil {
		return nil, fmt.Errorf("broker: sync %s: %w", req.File, err)
	}

	// Forward the LSP request verbatim; the caller-built params reference uri.
	var raw json.RawMessage
	if err := s.srv.Conn().Call(ctx, req.Method, req.Params, &raw); err != nil {
		return nil, fmt.Errorf("broker: %s: %w", req.Method, err)
	}
	return raw, nil
}

// resolveContext maps req to a project Context, honoring a Language override
// and falling back to the file's own directory when no resolver claims it but a
// language was named.
func (b *Broker) resolveContext(req QueryRequest) (projectcontext.Context, error) {
	if req.File == "" {
		return projectcontext.Context{}, fmt.Errorf("broker: query requires file")
	}
	if !filepath.IsAbs(req.File) {
		return projectcontext.Context{}, fmt.Errorf("broker: file must be absolute: %q", req.File)
	}
	if pc, ok := b.resolver.Resolve(req.File); ok {
		if req.Language != "" {
			pc.Language = req.Language
		}
		return pc, nil
	}
	if req.Language == "" {
		return projectcontext.Context{}, fmt.Errorf("broker: no resolver matched %q and no language given", req.File)
	}
	return projectcontext.Context{Root: filepath.Dir(req.File), Language: req.Language}, nil
}

// contentFor returns the document text to sync: the inline Text when supplied,
// else the file's on-disk bytes.
func (b *Broker) contentFor(req QueryRequest) (string, error) {
	if req.Text != nil {
		return *req.Text, nil
	}
	data, err := os.ReadFile(req.File)
	if err != nil {
		return "", fmt.Errorf("broker: read %s: %w", req.File, err)
	}
	return string(data), nil
}

// ensureSession returns the warm session for pc, performing a single-flight
// cold start when absent. Concurrent callers for the same key share one spawn.
func (b *Broker) ensureSession(pc projectcontext.Context) (*session, error) {
	key := pc.Key()

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, ErrClosed
	}
	if s, ok := b.sessions[key]; ok {
		b.mu.Unlock()
		<-s.ready // wait for the in-flight (or completed) cold start
		if s.initErr != nil {
			return nil, s.initErr
		}
		return s, nil
	}
	s := &session{
		pc:        pc,
		collector: lsp.NewDiagnosticsCollector(),
		ready:     make(chan struct{}),
		opened:    make(map[lsp.DocumentURI]int),
		lastUsed:  b.now(),
	}
	b.sessions[key] = s
	b.mu.Unlock()

	// Cold-start outside the broker lock so a slow spawn for one key does not
	// serialize ensureSession for other keys.
	err := b.startSession(s)
	if err != nil {
		// Drop the placeholder so a later call retries a fresh spawn.
		b.mu.Lock()
		if b.sessions[key] == s {
			delete(b.sessions, key)
		}
		b.mu.Unlock()
		s.initErr = err // publish before releasing the barrier
		close(s.ready)
		return nil, err
	}
	close(s.ready)
	return s, nil
}

// startSession spawns and initializes the language server for s.pc, publishing
// s.srv on success.
func (b *Broker) startSession(s *session) error {
	spec, err := b.specFor(s.pc)
	if err != nil {
		return err
	}
	if spec.Stderr == nil {
		spec.Stderr = b.stderr
	}
	srv, err := server.Launch(b.baseCtx, spec, s.collector)
	if err != nil {
		return fmt.Errorf("broker: launch %s: %w", s.pc.Language, err)
	}
	initCtx, cancel := context.WithTimeout(b.baseCtx, initTimeout)
	defer cancel()
	if _, err := srv.Client().Initialize(initCtx, b.initParams(s.pc)); err != nil {
		_ = srv.Close()
		return fmt.Errorf("broker: initialize %s: %w", s.pc.Language, err)
	}
	s.srv = srv
	return nil
}

// initParams is the initialize payload the broker sends every server. It
// advertises the read/write capabilities the multiplexed consumers need:
// hierarchical documentSymbol, publishDiagnostics, and workspaceEdit WITHOUT
// documentChanges — the latter forces servers to return rename edits in the
// "changes" map form that wsedit.FromWorkspaceEdit consumes.
func (b *Broker) initParams(pc projectcontext.Context) lsp.InitializeParams {
	return lsp.InitializeParams{
		ProcessID: os.Getpid(),
		RootURI:   lsp.DocumentURI("file://" + pc.Root),
		Capabilities: lsp.ClientCapabilities{
			Workspace: &lsp.WorkspaceClientCapabilities{
				WorkspaceEdit: &lsp.WorkspaceEditClientCapabilities{DocumentChanges: false},
			},
			TextDocument: &lsp.TextDocumentClientCapabilities{
				PublishDiagnostics: &lsp.PublishDiagnosticsClientCapabilities{},
				DocumentSymbol: &lsp.DocumentSymbolClientCapabilities{
					HierarchicalDocumentSymbolSupport: true,
				},
			},
		},
		ClientInfo: &lsp.ClientInfo{Name: "lspbridge-broker", Version: brokerVersion},
	}
}

// syncDoc opens the document on first sight, or sends a whole-document change
// on subsequent syncs, bumping the version each time.
func (s *session) syncDoc(ctx context.Context, uri lsp.DocumentURI, languageID, text string) error {
	s.mu.Lock()
	ver, open := s.opened[uri]
	if !open {
		s.opened[uri] = 1
		s.mu.Unlock()
		return s.srv.Client().DidOpen(ctx, lsp.DidOpenTextDocumentParams{
			TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: languageID, Version: 1, Text: text},
		})
	}
	ver++
	s.opened[uri] = ver
	s.mu.Unlock()
	return s.srv.Client().DidChange(ctx, lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri, Version: ver},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{Text: text}},
	})
}

// Status returns a snapshot of every warm session, sorted by key.
func (b *Broker) Status() StatusResponse {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	out := StatusResponse{Sessions: make([]SessionStatus, 0, len(b.sessions))}
	for key, s := range b.sessions {
		s.mu.Lock()
		docs := make([]string, 0, len(s.opened))
		for uri := range s.opened {
			docs = append(docs, string(uri))
		}
		sort.Strings(docs)
		out.Sessions = append(out.Sessions, SessionStatus{
			Key:         key,
			Language:    s.pc.Language,
			Root:        s.pc.Root,
			OpenDocs:    docs,
			IdleSeconds: now.Sub(s.lastUsed).Seconds(),
			Inflight:    s.inflight,
		})
		s.mu.Unlock()
	}
	sort.Slice(out.Sessions, func(i, j int) bool { return out.Sessions[i].Key < out.Sessions[j].Key })
	return out
}

// reapIdle shuts down and removes every session idle for at least idleTimeout
// and not currently serving a query. It returns the reaped contexts (for
// logging/tests). now is passed explicitly so tests drive it deterministically;
// the reaper loop passes b.now().
func (b *Broker) reapIdle(now time.Time) []projectcontext.Context {
	b.mu.Lock()
	var reap []*session
	var keys []string
	for key, s := range b.sessions {
		select {
		case <-s.ready:
		default:
			continue // still cold-starting; never reap mid-init
		}
		s.mu.Lock()
		idle := s.inflight == 0 && now.Sub(s.lastUsed) >= b.idleTimeout
		s.mu.Unlock()
		if idle && s.srv != nil {
			reap = append(reap, s)
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		delete(b.sessions, key)
	}
	b.mu.Unlock()

	reaped := make([]projectcontext.Context, 0, len(reap))
	for _, s := range reap {
		b.shutdownSession(s)
		reaped = append(reaped, s.pc)
	}
	return reaped
}

// reapLoop periodically reaps idle sessions until ctx is canceled. It is
// started by [Serve]; a Broker used as a bare library reaps only when its owner
// drives reapIdle.
func (b *Broker) reapLoop(ctx context.Context) {
	interval := b.idleTimeout / 4
	if interval < time.Second {
		interval = time.Second
	}
	if interval > 30*time.Second {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.reapIdle(b.now())
		}
	}
}

// shutdownSession performs an orderly LSP shutdown of one session's server.
func (b *Broker) shutdownSession(s *session) {
	if s.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
}

// ShutdownAll orderly-shuts every warm session and marks the broker closed;
// subsequent Query calls return [ErrClosed]. It is idempotent and is what the
// daemon calls on SIGTERM.
func (b *Broker) ShutdownAll() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	all := make([]*session, 0, len(b.sessions))
	for key, s := range b.sessions {
		all = append(all, s)
		delete(b.sessions, key)
	}
	b.mu.Unlock()

	for _, s := range all {
		<-s.ready // let any in-flight cold start settle before teardown
		b.shutdownSession(s)
	}
	b.cancel() // cancel the lifetime context; reaps any lingering subprocess
}
