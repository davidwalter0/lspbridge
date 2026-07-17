package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// TestPyrightLiveDiagnostics is the P0.2b live proof: launch the real
// pyright-langserver, run the LSP handshake, open a Python file with a
// definite type error, and confirm pyright reports it back over
// textDocument/publishDiagnostics. It is skipped (not failed) when pyright
// isn't installed, so `make test` stays green on machines without it.
func TestPyrightLiveDiagnostics(t *testing.T) {
	if _, err := exec.LookPath(PyrightCommand); err != nil {
		t.Skipf("pyright not installed (%v); skipping live integration test", err)
	}

	dir := t.TempDir()
	const badPy = "x: int = \"not an int\"\n"
	pyPath := filepath.Join(dir, "bad.py")
	if err := os.WriteFile(pyPath, []byte(badPy), 0o644); err != nil {
		t.Fatalf("write bad.py: %v", err)
	}

	// pyright can be slow to warm up (indexing, environment discovery), so
	// give the whole handshake a generous ceiling.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	spec, err := PyrightSpec(dir)
	if err != nil {
		t.Fatalf("PyrightSpec: %v", err)
	}

	collector := lsp.NewDiagnosticsCollector()
	srv, err := Launch(ctx, spec, collector)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer func() { _ = srv.Close() }()

	res, err := srv.Client().Initialize(ctx, lsp.InitializeParams{
		ProcessID: os.Getpid(),
		RootURI:   lsp.DocumentURI("file://" + dir),
		Capabilities: lsp.ClientCapabilities{
			TextDocument: &lsp.TextDocumentClientCapabilities{
				PublishDiagnostics: &lsp.PublishDiagnosticsClientCapabilities{},
			},
		},
		ClientInfo: &lsp.ClientInfo{Name: "lspbridge-live-test", Version: "0.0.1"},
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if res.ServerInfo != nil {
		t.Logf("pyright server info: %s %s", res.ServerInfo.Name, res.ServerInfo.Version)
	}

	uri := lsp.DocumentURI("file://" + pyPath)
	if err := srv.Client().DidOpen(ctx, lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        uri,
			LanguageID: "python",
			Version:    1,
			Text:       badPy,
		},
	}); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 30*time.Second)
	defer waitCancel()
	diags, err := collector.Wait(waitCtx, uri)
	if err != nil {
		t.Fatalf("Wait for publishDiagnostics: %v", err)
	}
	if len(diags) == 0 {
		t.Fatal("expected at least one diagnostic from pyright, got none")
	}
	t.Logf("pyright reported %d diagnostic(s); first: %s", len(diags), diags[0].Message)

	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
