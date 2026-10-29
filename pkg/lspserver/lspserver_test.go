package lspserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

func TestPositionConversionsSurrogates(t *testing.T) {
	// "a😀b\nあc\r\nx": 😀 is 4 bytes / 2 units, あ is 3 bytes / 1 unit.
	text := []byte("a😀b\nあc\r\nx")
	cases := []struct {
		pos lsp.Position
		off int
	}{
		{lsp.Position{Line: 0, Character: 0}, 0},
		{lsp.Position{Line: 0, Character: 1}, 1},
		{lsp.Position{Line: 0, Character: 2}, 1}, // mid-pair rounds down
		{lsp.Position{Line: 0, Character: 3}, 5},
		{lsp.Position{Line: 0, Character: 4}, 6},
		{lsp.Position{Line: 0, Character: 99}, 6}, // clamp to line end
		{lsp.Position{Line: 1, Character: 1}, 10},
		{lsp.Position{Line: 1, Character: 2}, 11},
		{lsp.Position{Line: 1, Character: 99}, 11}, // before \r\n
		{lsp.Position{Line: 2, Character: 1}, 14},
		{lsp.Position{Line: 9, Character: 0}, 14}, // past end clamps to EOF
	}
	for _, c := range cases {
		if got := OffsetAt(text, c.pos); got != c.off {
			t.Errorf("OffsetAt(%v) = %d, want %d", c.pos, got, c.off)
		}
	}
	back := []struct {
		off int
		pos lsp.Position
	}{
		{0, pp(0, 0)}, {1, pp(0, 1)}, {5, pp(0, 3)},
		{6, pp(0, 4)}, {7, pp(1, 0)}, {10, pp(1, 1)},
		{14, pp(2, 1)}, {99, pp(2, 1)}, {3, pp(0, 1)}, {2, pp(0, 1)},
	}
	for _, c := range back {
		if got := PositionAt(text, c.off); got != c.pos {
			t.Errorf("PositionAt(%d) = %v, want %v", c.off, got, c.pos)
		}
	}
	// Round trip on every rune boundary.
	for off := 0; off <= len(text); off++ {
		if off < len(text) && (!utf8.RuneStart(text[off]) || (text[off] == '\n' && text[off-1] == '\r')) {
			continue
		}
		p := PositionAt(text, off)
		if o2 := OffsetAt(text, p); o2 > off || PositionAt(text, o2) != p {
			t.Errorf("round trip off=%d pos=%v back=%d", off, p, o2)
		}
	}
}

func rng(l1, c1, l2, c2 int) *lsp.Range {
	return &lsp.Range{Start: lsp.Position{Line: l1, Character: c1}, End: lsp.Position{Line: l2, Character: c2}}
}

func TestStoreIncrementalWithEmoji(t *testing.T) {
	s := NewStore()
	uri := lsp.DocumentURI("file:///t.txt")
	s.Open(lsp.TextDocumentItem{URI: uri, LanguageID: "text", Version: 1, Text: "a😀b\nline2\n"})

	// Replace "b" (after the emoji, utf-16 col 3..4) with "XY", then insert
	// "Z" at the start of line 1 (relative to the already-edited text).
	ch := s.Apply(lsp.DidChangeTextDocumentParams{
		TextDocument: lsp.VersionedTextDocumentIdentifier{URI: uri, Version: 2},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{
			{Range: rng(0, 3, 0, 4), Text: "XY"},
			{Range: rng(1, 0, 1, 0), Text: "Z"},
		},
	})
	if got := string(ch.New.Text); got != "a😀XY\nZline2\n" {
		t.Fatalf("text = %q", got)
	}
	if ch.New.Version != 2 || ch.Old.Version != 1 {
		t.Fatalf("versions %d/%d", ch.Old.Version, ch.New.Version)
	}
	if len(ch.Edits) != 2 {
		t.Fatalf("edits = %+v", ch.Edits)
	}
	e := ch.Edits[0]
	if e.StartByte != 5 || e.OldEndByte != 6 || e.NewEndByte != 7 ||
		e.StartPoint != (Point{0, 5}) || e.OldEndPoint != (Point{0, 6}) || e.NewEndPoint != (Point{0, 7}) {
		t.Errorf("edit0 = %+v", e)
	}
	e = ch.Edits[1]
	if e.StartByte != 8 || e.NewEndByte != 9 || e.StartPoint != (Point{1, 0}) || e.NewEndPoint != (Point{1, 1}) {
		t.Errorf("edit1 = %+v", e)
	}
	// The old snapshot is untouched.
	if string(ch.Old.Text) != "a😀b\nline2\n" {
		t.Errorf("old snapshot mutated: %q", ch.Old.Text)
	}

	// A full replacement mixes with ranged ones.
	ch = s.Apply(lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri, Version: 3},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{Text: "fresh"}},
	})
	if !ch.Full || string(ch.New.Text) != "fresh" || len(ch.Edits) != 0 {
		t.Errorf("full change = %+v", ch)
	}
	// Multi-line deletion across a line break.
	s.Apply(lsp.DidChangeTextDocumentParams{TextDocument: lsp.VersionedTextDocumentIdentifier{URI: uri, Version: 4},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{Text: "ab\ncd\nef"}}})
	ch = s.Apply(lsp.DidChangeTextDocumentParams{TextDocument: lsp.VersionedTextDocumentIdentifier{URI: uri, Version: 5},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{Range: rng(0, 1, 2, 1), Text: ""}}})
	if string(ch.New.Text) != "af" {
		t.Errorf("multi-line delete = %q", ch.New.Text)
	}
	if got := s.Apply(lsp.DidChangeTextDocumentParams{TextDocument: lsp.VersionedTextDocumentIdentifier{URI: "file:///nope"}}); got != nil {
		t.Errorf("change to unopened doc should be nil")
	}
}

// client is a bare jsonrpc peer over an in-memory pipe.
func startServer(t *testing.T, opts Options) (*jsonrpc.Conn, <-chan int, chan *jsonrpc.Request) {
	t.Helper()
	cs, ss := net.Pipe()
	notes := make(chan *jsonrpc.Request, 32)
	cl := jsonrpc.NewConn(cs, jsonrpc.HandlerFunc(func(_ context.Context, r *jsonrpc.Request) (any, error) {
		notes <- r
		return nil, nil
	}))
	t.Cleanup(func() { _ = cl.Close() })
	code := make(chan int, 1)
	go func() { code <- Serve(context.Background(), ss, opts) }()
	return cl, code, notes
}

func call(t *testing.T, c *jsonrpc.Conn, method string, params, res any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Call(ctx, method, params, res)
}

func TestLifecycle(t *testing.T) {
	var opened, changed, closed int
	cl, code, notes := startServer(t, Options{
		Info:         &lsp.ServerInfo{Name: "t", Version: "1"},
		Capabilities: map[string]any{"documentSymbolProvider": true},
		Handler: jsonrpc.HandlerFunc(func(_ context.Context, r *jsonrpc.Request) (any, error) {
			if r.Method == "x/echo" {
				return "pong", nil
			}
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: r.Method}
		}),
		OnOpen: func(ctx context.Context, s *Server, d *Doc) {
			opened++
			_ = s.PublishDiagnostics(ctx, d.URI, d.Version, nil)
		},
		OnChange: func(context.Context, *Server, *Change) { changed++ },
		OnClose:  func(context.Context, *Server, *Doc) { closed++ },
	})

	// Before initialize: request -> ServerNotInitialized.
	var rpcErr *jsonrpc.Error
	err := call(t, cl, "x/echo", nil, nil)
	if !errors.As(err, &rpcErr) || rpcErr.Code != jsonrpc.CodeServerNotInitialized {
		t.Fatalf("pre-initialize err = %v", err)
	}
	// A notification before initialize is dropped.
	_ = cl.Notify(context.Background(), "textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: "file:///early", Text: "x"}})

	var res lsp.InitializeResult
	if err := call(t, cl, "initialize", lsp.InitializeParams{}, &res); err != nil {
		t.Fatal(err)
	}
	if res.ServerInfo == nil || res.ServerInfo.Name != "t" {
		t.Errorf("serverInfo = %+v", res.ServerInfo)
	}
	var caps map[string]json.RawMessage
	if err := json.Unmarshal(res.Capabilities, &caps); err != nil {
		t.Fatal(err)
	}
	if string(caps["positionEncoding"]) != `"utf-16"` || caps["textDocumentSync"] == nil || string(caps["documentSymbolProvider"]) != "true" {
		t.Errorf("caps = %s", res.Capabilities)
	}
	if err := call(t, cl, "initialize", lsp.InitializeParams{}, nil); err == nil {
		t.Error("second initialize must fail")
	}
	_ = cl.Notify(context.Background(), "initialized", struct{}{})

	var echo string
	if err := call(t, cl, "x/echo", nil, &echo); err != nil || echo != "pong" {
		t.Fatalf("echo = %q, %v", echo, err)
	}

	uri := lsp.DocumentURI("file:///a.txt")
	_ = cl.Notify(context.Background(), "textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "text", Version: 1, Text: "hi"}})
	select {
	case n := <-notes:
		var p lsp.PublishDiagnosticsParams
		_ = json.Unmarshal(n.Params, &p)
		if n.Method != "textDocument/publishDiagnostics" || p.URI != uri || !bytes.Contains(n.Params, []byte(`"diagnostics":[]`)) {
			t.Errorf("notification = %s %s", n.Method, n.Params)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no publishDiagnostics")
	}
	_ = cl.Notify(context.Background(), "textDocument/didChange", lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri, Version: 2},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{Text: "ho"}}})
	_ = cl.Notify(context.Background(), "textDocument/didClose", lsp.DidCloseTextDocumentParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri}})

	if err := call(t, cl, "shutdown", nil, nil); err != nil {
		t.Fatal(err)
	}
	// Requests after shutdown are invalid.
	err = call(t, cl, "x/echo", nil, nil)
	if !errors.As(err, &rpcErr) || rpcErr.Code != jsonrpc.CodeInvalidRequest {
		t.Errorf("post-shutdown err = %v", err)
	}
	_ = cl.Notify(context.Background(), "exit", nil)
	select {
	case c := <-code:
		if c != 0 {
			t.Errorf("exit code after shutdown = %d, want 0", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit")
	}
	if opened != 1 || changed != 1 || closed != 1 {
		t.Errorf("hooks open/change/close = %d/%d/%d (the pre-initialize open must be dropped)", opened, changed, closed)
	}
}

func TestExitWithoutShutdownIsOne(t *testing.T) {
	cl, code, _ := startServer(t, Options{})
	if err := call(t, cl, "initialize", lsp.InitializeParams{}, nil); err != nil {
		t.Fatal(err)
	}
	_ = cl.Notify(context.Background(), "exit", nil)
	select {
	case c := <-code:
		if c != 1 {
			t.Errorf("code = %d, want 1", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no exit")
	}
}

func TestConnectionLossIsOne(t *testing.T) {
	cl, code, _ := startServer(t, Options{})
	_ = cl.Close()
	select {
	case c := <-code:
		if c != 1 {
			t.Errorf("code = %d", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not notice the closed connection")
	}
}

func TestCancelRequest(t *testing.T) {
	started := make(chan struct{})
	cl, _, _ := startServer(t, Options{
		Handler: jsonrpc.HandlerFunc(func(ctx context.Context, r *jsonrpc.Request) (any, error) {
			close(started)
			<-ctx.Done()
			return "late", ctx.Err()
		}),
	})
	if err := call(t, cl, "initialize", lsp.InitializeParams{}, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- cl.Call(ctx, "x/slow", nil, nil) }()
	<-started
	cancel() // the client Call sends $/cancelRequest
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not unblock the call")
	}
	// The server stays healthy.
	if err := call(t, cl, "shutdown", nil, nil); err != nil {
		t.Errorf("shutdown after cancel: %v", err)
	}
}

var _ io.Closer = (*jsonrpc.Conn)(nil)

func pp(l, c int) lsp.Position { return lsp.Position{Line: l, Character: c} }
