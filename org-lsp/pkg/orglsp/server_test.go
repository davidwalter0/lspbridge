package orglsp

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// testConn wires a Server behind one end of an in-memory net.Pipe (mirroring
// cmd/fakelsp's own proven pattern — see the lspbridge todo this
// implements) and returns the "client" jsonrpc.Conn a test drives requests
// through, plus the DiagnosticsCollector that receives any
// textDocument/publishDiagnostics notifications the server sends.
func testConn(t *testing.T) (*jsonrpc.Conn, *lsp.DiagnosticsCollector, *Server) {
	t.Helper()
	srv := NewServer()
	collector := lsp.NewDiagnosticsCollector()

	clientSide, serverSide := net.Pipe()
	serverConn := jsonrpc.NewConn(serverSide, srv)
	clientConn := jsonrpc.NewConn(clientSide, collector)
	srv.SetNotifier(func(ctx context.Context, method string, params any) {
		_ = serverConn.Notify(ctx, method, params)
	})
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return clientConn, collector, srv
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestInitializeAdvertisesHonestCapabilities(t *testing.T) {
	conn, _, _ := testConn(t)
	var result lsp.InitializeResult
	if err := conn.Call(testCtx(t), "initialize", lsp.InitializeParams{RootURI: "file:///workspace"}, &result); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if result.ServerInfo == nil || result.ServerInfo.Name != "org-lsp" {
		t.Fatalf("ServerInfo = %+v, want Name=org-lsp", result.ServerInfo)
	}
	var caps struct {
		TextDocumentSync        int  `json:"textDocumentSync"`
		DocumentSymbolProvider  bool `json:"documentSymbolProvider"`
		FoldingRangeProvider    bool `json:"foldingRangeProvider"`
		WorkspaceSymbolProvider bool `json:"workspaceSymbolProvider"`
		// Deliberately absent capabilities that would signal an unimplemented
		// method — if any of these ever decode true, org-lsp is advertising
		// something it doesn't do.
		HoverProvider      bool `json:"hoverProvider"`
		RenameProvider     bool `json:"renameProvider"`
		DefinitionProvider bool `json:"definitionProvider"`
		ReferencesProvider bool `json:"referencesProvider"`
		CompletionProvider bool `json:"completionProvider"`
	}
	if err := json.Unmarshal(result.Capabilities, &caps); err != nil {
		t.Fatalf("unmarshal capabilities: %v", err)
	}
	if caps.TextDocumentSync != 1 {
		t.Errorf("TextDocumentSync = %d, want 1 (Full)", caps.TextDocumentSync)
	}
	if !caps.DocumentSymbolProvider || !caps.FoldingRangeProvider || !caps.WorkspaceSymbolProvider {
		t.Errorf("caps = %+v, want documentSymbol/foldingRange/workspaceSymbol all true", caps)
	}
	if caps.HoverProvider || caps.RenameProvider || caps.DefinitionProvider || caps.ReferencesProvider || caps.CompletionProvider {
		t.Errorf("caps = %+v, want no unimplemented-method capability set", caps)
	}
}

func TestDidOpenThenDocumentSymbol(t *testing.T) {
	conn, _, _ := testConn(t)
	ctx := testCtx(t)
	if err := conn.Call(ctx, "initialize", lsp.InitializeParams{}, nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	text := readFixture(t)
	err := conn.Notify(ctx, "textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: "file:///doc.org", LanguageID: "org", Version: 1, Text: text},
	})
	if err != nil {
		t.Fatalf("didOpen: %v", err)
	}

	var raw json.RawMessage
	if err := conn.Call(ctx, "textDocument/documentSymbol", lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///doc.org"},
	}, &raw); err != nil {
		t.Fatalf("documentSymbol: %v", err)
	}
	var syms []lsp.DocumentSymbol
	if err := json.Unmarshal(raw, &syms); err != nil {
		t.Fatalf("unmarshal documentSymbol result: %v", err)
	}
	if len(syms) != 3 || syms[0].Name != "Session State" {
		t.Fatalf("syms = %+v, want the 3-root golden tree (see TestDocumentSymbolsGoldenTree)", syms)
	}
}

func TestDocumentSymbolUnopenedDocumentIsNull(t *testing.T) {
	conn, _, _ := testConn(t)
	ctx := testCtx(t)
	if err := conn.Call(ctx, "initialize", lsp.InitializeParams{}, nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	var raw json.RawMessage
	if err := conn.Call(ctx, "textDocument/documentSymbol", lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///never-opened.org"},
	}, &raw); err != nil {
		t.Fatalf("documentSymbol: %v", err)
	}
	if string(raw) != "null" {
		t.Errorf("result = %s, want null", raw)
	}
}

func TestFoldingRangeOverWire(t *testing.T) {
	conn, _, _ := testConn(t)
	ctx := testCtx(t)
	_ = conn.Call(ctx, "initialize", lsp.InitializeParams{}, nil)
	// Lines: 0 "* One", 1 "body", 2 "** Two", 3 "more", 4 "" (trailing nl).
	// Both headings fold to EOF (line 4): "Two" because "more" follows it,
	// and "One" because "Two" (level 2, deeper) never closes a level-1 span.
	text := "* One\nbody\n** Two\nmore\n"
	_ = conn.Notify(ctx, "textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: "file:///f.org", Text: text},
	})
	var raw json.RawMessage
	if err := conn.Call(ctx, "textDocument/foldingRange", lsp.FoldingRangeParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file:///f.org"},
	}, &raw); err != nil {
		t.Fatalf("foldingRange: %v", err)
	}
	var ranges []lsp.FoldingRange
	if err := json.Unmarshal(raw, &ranges); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ranges) != 2 {
		t.Fatalf("ranges = %+v, want 2 folds (One: 0-4, Two: 2-4)", ranges)
	}
	if ranges[0].StartLine != 0 || ranges[0].EndLine != 4 {
		t.Errorf("ranges[0] = %+v, want StartLine=0 EndLine=4 ('One')", ranges[0])
	}
	if ranges[1].StartLine != 2 || ranges[1].EndLine != 4 {
		t.Errorf("ranges[1] = %+v, want StartLine=2 EndLine=4 ('Two')", ranges[1])
	}
}

func TestWorkspaceSymbolOverWire(t *testing.T) {
	root := t.TempDir()
	writeOrgFile(t, filepath.Join(root, "a.org"), "* Findable Heading\n")

	conn, _, _ := testConn(t)
	ctx := testCtx(t)
	if err := conn.Call(ctx, "initialize", lsp.InitializeParams{RootURI: lsp.DocumentURI("file://" + root)}, nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	var raw json.RawMessage
	if err := conn.Call(ctx, "workspace/symbol", lsp.WorkspaceSymbolParams{Query: "findable"}, &raw); err != nil {
		t.Fatalf("workspace/symbol: %v", err)
	}
	var syms []lsp.SymbolInformation
	if err := json.Unmarshal(raw, &syms); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(syms) != 1 || syms[0].Name != "Findable Heading" {
		t.Fatalf("syms = %+v", syms)
	}
}

func TestWorkspaceSymbolBeforeInitializeIsEmpty(t *testing.T) {
	conn, _, _ := testConn(t)
	var raw json.RawMessage
	if err := conn.Call(testCtx(t), "workspace/symbol", lsp.WorkspaceSymbolParams{Query: "x"}, &raw); err != nil {
		t.Fatalf("workspace/symbol: %v", err)
	}
	var syms []lsp.SymbolInformation
	if err := json.Unmarshal(raw, &syms); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(syms) != 0 {
		t.Errorf("syms = %+v, want none (no root set yet)", syms)
	}
}

func TestDidChangePublishesDiagnosticsAndClearsThem(t *testing.T) {
	conn, collector, _ := testConn(t)
	ctx := testCtx(t)
	_ = conn.Call(ctx, "initialize", lsp.InitializeParams{}, nil)

	uri := lsp.DocumentURI("file:///d.org")
	broken := "[[file:does-not-exist.png]]\n"
	if err := conn.Notify(ctx, "textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, Text: broken, Version: 1},
	}); err != nil {
		t.Fatalf("didOpen: %v", err)
	}
	diags, err := collector.Wait(ctx, uri)
	if err != nil {
		t.Fatalf("waiting for first diagnostics: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want 1 (broken link)", diags)
	}

	// Fix the document; the server must publish a fresh (empty) batch.
	fixed := "prose, no links\n"
	if err := conn.Notify(ctx, "textDocument/didChange", lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri, Version: 2},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{Text: fixed}},
	}); err != nil {
		t.Fatalf("didChange: %v", err)
	}
	// Poll until the batch actually changes to empty (Wait only blocks for
	// the first NON-empty batch, which already arrived above).
	deadline := time.Now().Add(2 * time.Second)
	for {
		if d, ok := collector.Diagnostics(uri); ok && len(d) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the cleared (empty) diagnostics batch")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDidCloseForgetsBuffer(t *testing.T) {
	conn, _, _ := testConn(t)
	ctx := testCtx(t)
	_ = conn.Call(ctx, "initialize", lsp.InitializeParams{}, nil)
	uri := lsp.DocumentURI("file:///c.org")
	_ = conn.Notify(ctx, "textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, Text: "* H\n"},
	})
	_ = conn.Notify(ctx, "textDocument/didClose", struct {
		TextDocument lsp.TextDocumentIdentifier `json:"textDocument"`
	}{TextDocument: lsp.TextDocumentIdentifier{URI: uri}})

	// didClose is a notification; give the (mutex-serialized) handler a
	// moment before asserting the buffer is gone.
	deadline := time.Now().Add(2 * time.Second)
	for {
		var raw json.RawMessage
		if err := conn.Call(ctx, "textDocument/documentSymbol", lsp.DocumentSymbolParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		}, &raw); err != nil {
			t.Fatalf("documentSymbol: %v", err)
		} else if string(raw) == "null" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("didClose never took effect")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUnknownRequestMethodIsMethodNotFound(t *testing.T) {
	conn, _, _ := testConn(t)
	err := conn.Call(testCtx(t), "textDocument/definitelyNotARealMethod", nil, nil)
	if err == nil {
		t.Fatal("want an error for an unimplemented request method")
	}
	rpcErr, ok := err.(*jsonrpc.Error)
	if !ok {
		t.Fatalf("err = %T(%v), want *jsonrpc.Error", err, err)
	}
	if rpcErr.Code != jsonrpc.CodeMethodNotFound {
		t.Errorf("Code = %d, want %d (CodeMethodNotFound)", rpcErr.Code, jsonrpc.CodeMethodNotFound)
	}
}

func TestUnknownNotificationIsSilentlyAcked(t *testing.T) {
	conn, _, _ := testConn(t)
	ctx := testCtx(t)
	if err := conn.Notify(ctx, "$/some/unknown/notification", nil); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	// The connection must still be alive and answering afterward.
	var result lsp.InitializeResult
	if err := conn.Call(ctx, "initialize", lsp.InitializeParams{}, &result); err != nil {
		t.Fatalf("initialize after unknown notification: %v", err)
	}
}

func TestShutdownThenExitIsClean(t *testing.T) {
	conn, _, srv := testConn(t)
	ctx := testCtx(t)
	exitc := make(chan bool, 1)
	srv.SetOnExit(func(clean bool) { exitc <- clean })

	if err := conn.Call(ctx, "shutdown", nil, nil); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := conn.Notify(ctx, "exit", nil); err != nil {
		t.Fatalf("exit: %v", err)
	}
	select {
	case clean := <-exitc:
		if !clean {
			t.Error("onExit(false), want true (shutdown preceded exit)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("onExit was never called")
	}
}

func TestExitWithoutShutdownIsUnclean(t *testing.T) {
	conn, _, srv := testConn(t)
	ctx := testCtx(t)
	exitc := make(chan bool, 1)
	srv.SetOnExit(func(clean bool) { exitc <- clean })

	if err := conn.Notify(ctx, "exit", nil); err != nil {
		t.Fatalf("exit: %v", err)
	}
	select {
	case clean := <-exitc:
		if clean {
			t.Error("onExit(true), want false (exit without a prior shutdown)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("onExit was never called")
	}
}

func TestInitializeMalformedParams(t *testing.T) {
	conn, _, _ := testConn(t)
	err := conn.Call(testCtx(t), "initialize", json.RawMessage(`{"rootUri":42}`), nil)
	if err == nil {
		t.Fatal("want an error for malformed initialize params")
	}
	rpcErr, ok := err.(*jsonrpc.Error)
	if !ok || rpcErr.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("err = %v, want *jsonrpc.Error{Code: CodeInvalidParams}", err)
	}
}
