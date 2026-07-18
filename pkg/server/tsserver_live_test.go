package server_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/lspbridge/pkg/server"
)

const fixtureTS = `function greet(name: string): string {
    return "hi " + name;
}

function main(): void {
    console.log(greet("a"));
    console.log(greet("b"));
}
`

const fixtureTSConfig = `{
  "compilerOptions": {
    "target": "ES2020",
    "module": "commonjs",
    "strict": true
  }
}
`

const fixturePackageJSON = `{
  "name": "lspbridge-ts-fixture",
  "version": "1.0.0",
  "private": true
}
`

// startTSServer launches real typescript-language-server over a fixture Node
// project (package.json + tsconfig.json — the Node ProjectContext markers)
// and completes the LSP handshake. It skips the test when
// typescript-language-server is not installed.
func startTSServer(t *testing.T, ctx context.Context) (*server.Server, lsp.DocumentURI, string) {
	t.Helper()
	if _, err := exec.LookPath(server.TypeScriptLanguageServerCommand); err != nil {
		t.Skipf("typescript-language-server not installed (%v); skipping live proof", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(fixturePackageJSON), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(fixtureTSConfig), 0o644); err != nil {
		t.Fatalf("write tsconfig.json: %v", err)
	}
	tsPath := filepath.Join(dir, "mod.ts")
	if err := os.WriteFile(tsPath, []byte(fixtureTS), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	spec, err := server.TypeScriptLanguageServerSpec(dir)
	if err != nil {
		t.Fatalf("TypeScriptLanguageServerSpec: %v", err)
	}
	srv, err := server.Launch(ctx, spec, lsp.NewDiagnosticsCollector())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	if _, err := srv.Client().Initialize(ctx, lsp.InitializeParams{
		ProcessID: os.Getpid(),
		RootURI:   lsp.DocumentURI("file://" + dir),
		Capabilities: lsp.ClientCapabilities{
			Workspace: &lsp.WorkspaceClientCapabilities{
				WorkspaceEdit: &lsp.WorkspaceEditClientCapabilities{DocumentChanges: false},
			},
			TextDocument: &lsp.TextDocumentClientCapabilities{
				PublishDiagnostics: &lsp.PublishDiagnosticsClientCapabilities{},
				DocumentSymbol:     &lsp.DocumentSymbolClientCapabilities{HierarchicalDocumentSymbolSupport: true},
			},
		},
		ClientInfo: &lsp.ClientInfo{Name: "lspbridge-tsserver-live", Version: "0.0.1"},
	}); err != nil {
		_ = srv.Close()
		t.Fatalf("Initialize: %v", err)
	}

	uri := lsp.DocumentURI("file://" + tsPath)
	if err := srv.Client().DidOpen(ctx, lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "typescript", Version: 1, Text: fixtureTS},
	}); err != nil {
		_ = srv.Close()
		t.Fatalf("DidOpen: %v", err)
	}
	return srv, uri, tsPath
}

// waitForTSSymbols polls documentSymbol until typescript-language-server has
// analyzed the file (returns at least one symbol) or the deadline passes.
func waitForTSSymbols(t *testing.T, ctx context.Context, srv *server.Server, uri lsp.DocumentURI) *lsp.DocumentSymbolResult {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for {
		res, err := srv.Client().DocumentSymbol(ctx, lsp.DocumentSymbolParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		})
		if err != nil {
			t.Fatalf("DocumentSymbol: %v", err)
		}
		if len(res.Hierarchical) > 0 || len(res.Flat) > 0 {
			return res
		}
		if time.Now().After(deadline) {
			t.Fatal("typescript-language-server returned no symbols before deadline")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func tsSymbolNames(res *lsp.DocumentSymbolResult) []string {
	var names []string
	for _, s := range res.Hierarchical {
		names = append(names, s.Name)
	}
	for _, s := range res.Flat {
		names = append(names, s.Name)
	}
	return names
}

func TestTSServerLiveDocumentSymbol(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv, uri, _ := startTSServer(t, ctx)
	defer func() { _ = srv.Close() }()

	res := waitForTSSymbols(t, ctx, srv, uri)
	names := tsSymbolNames(res)
	t.Logf("documentSymbol returned: %v (hierarchical=%d flat=%d)", names, len(res.Hierarchical), len(res.Flat))
	for _, want := range []string{"greet", "main"} {
		if !tsContains(names, want) {
			t.Errorf("documentSymbol missing %q; got %v", want, names)
		}
	}
}

func tsContains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
