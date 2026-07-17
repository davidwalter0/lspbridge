package lsp

import (
	"context"
	"encoding/json"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
)

// fakeServer records the methods it receives and answers initialize/shutdown.
type fakeServer struct {
	methods chan string
}

func (s *fakeServer) Handle(_ context.Context, req *jsonrpc.Request) (any, error) {
	// Non-blocking record; the channel is buffered generously.
	select {
	case s.methods <- req.Method:
	default:
	}
	switch req.Method {
	case "initialize":
		return InitializeResult{
			Capabilities: json.RawMessage(`{"textDocumentSync":1}`),
			ServerInfo:   &ServerInfo{Name: "fake", Version: "0.0.1"},
		}, nil
	case "shutdown":
		return nil, nil
	default:
		return nil, nil
	}
}

func newClientWithFakeServer(t *testing.T) (*Client, *fakeServer) {
	t.Helper()
	a, b := net.Pipe()
	fs := &fakeServer{methods: make(chan string, 16)}
	serverConn := jsonrpc.NewConn(b, fs)
	clientConn := jsonrpc.NewConn(a, nil)
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return NewClient(clientConn), fs
}

func TestClientInitialize(t *testing.T) {
	client, _ := newClientWithFakeServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := client.Initialize(ctx, InitializeParams{
		ProcessID:  1234,
		RootURI:    "file:///proj",
		ClientInfo: &ClientInfo{Name: "lspbridge", Version: "test"},
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if res.ServerInfo == nil || res.ServerInfo.Name != "fake" {
		t.Fatalf("serverInfo = %+v", res.ServerInfo)
	}
	// Raw capabilities are retained verbatim.
	if string(res.Capabilities) != `{"textDocumentSync":1}` {
		t.Errorf("capabilities = %s", res.Capabilities)
	}
}

func TestClientLifecycleSequence(t *testing.T) {
	client, fs := newClientWithFakeServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := client.Initialize(ctx, InitializeParams{RootURI: "file:///p"}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := client.DidOpen(ctx, DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: "file:///p/a.py", LanguageID: "python", Version: 1, Text: "x = 1\n"},
	}); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	if err := client.DidChange(ctx, DidChangeTextDocumentParams{
		TextDocument:   VersionedTextDocumentIdentifier{URI: "file:///p/a.py", Version: 2},
		ContentChanges: []TextDocumentContentChangeEvent{{Text: "x = 2\n"}},
	}); err != nil {
		t.Fatalf("DidChange: %v", err)
	}
	if err := client.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	got := collectMethods(t, fs, 5)
	want := []string{"initialize", "initialized", "textDocument/didOpen", "textDocument/didChange", "shutdown"}
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("server never received %q; got %v", w, got)
		}
	}
}

func collectMethods(t *testing.T, fs *fakeServer, n int) []string {
	t.Helper()
	var got []string
	deadline := time.After(2 * time.Second)
	for len(got) < n {
		select {
		case m := <-fs.methods:
			got = append(got, m)
		case <-deadline:
			return got
		}
	}
	return got
}
