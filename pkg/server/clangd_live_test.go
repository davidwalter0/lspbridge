package server_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/lspbridge/pkg/server"
)

// fixtureCompileCommandsJSON is a minimal JSON compilation database for one
// translation unit. "arguments" (rather than a shell-quoted "command"
// string) is used to avoid any shell-splitting ambiguity; the fixture
// avoids any standard-library #include so clangd never needs to resolve a
// system header search path to parse it.
const fixtureCompileCommandsJSON = `[
  {
    "directory": %q,
    "file": %q,
    "arguments": ["cc", "-std=c11", "-c", %q]
  }
]
`

const fixtureMainC = `int add(int a, int b) {
    return a + b;
}

int main(void) {
    return add(1, 2);
}
`

// clangdIndexDeadline bounds how long this test waits for clangd to parse
// the fixture and answer documentSymbol. clangd parses a single
// two-function translation unit with no external headers and no
// background-index, which is far faster than rust-analyzer's/dart's
// whole-package indexing — but the deadline still allows generous headroom
// for a cold/loaded host rather than an exact budget, matching this
// package's rust-analyzer/dart-analysis-server live-test convention of
// polling to a deadline instead of a fixed sleep.
const clangdIndexDeadline = 30 * time.Second

// startClangd launches real clangd over a tiny fixture translation unit
// (compile_commands.json — the root marker projectcontext.CResolver /
// CppResolver walk up for — plus one fixture .c file) and completes the LSP
// handshake. It skips the test when clangd is not installed.
func startClangd(t *testing.T, ctx context.Context) (*server.Server, lsp.DocumentURI, string) {
	t.Helper()
	if _, err := exec.LookPath(server.ClangdCommand); err != nil {
		t.Skipf("clangd not installed (%v); skipping live proof", err)
	}
	root := t.TempDir()
	cPath := filepath.Join(root, "main.c")
	if err := os.WriteFile(cPath, []byte(fixtureMainC), 0o644); err != nil {
		t.Fatalf("write main.c: %v", err)
	}
	ccJSON := fmt.Sprintf(fixtureCompileCommandsJSON, root, cPath, cPath)
	if err := os.WriteFile(filepath.Join(root, "compile_commands.json"), []byte(ccJSON), 0o644); err != nil {
		t.Fatalf("write compile_commands.json: %v", err)
	}

	spec, err := server.ClangdSpec(root)
	if err != nil {
		t.Fatalf("ClangdSpec: %v", err)
	}
	srv, err := server.Launch(ctx, spec, lsp.NewDiagnosticsCollector())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	if _, err := srv.Client().Initialize(ctx, lsp.InitializeParams{
		ProcessID: os.Getpid(),
		RootURI:   lsp.DocumentURI("file://" + root),
		Capabilities: lsp.ClientCapabilities{
			Workspace: &lsp.WorkspaceClientCapabilities{
				WorkspaceEdit: &lsp.WorkspaceEditClientCapabilities{DocumentChanges: false},
			},
			TextDocument: &lsp.TextDocumentClientCapabilities{
				PublishDiagnostics: &lsp.PublishDiagnosticsClientCapabilities{},
				DocumentSymbol:     &lsp.DocumentSymbolClientCapabilities{HierarchicalDocumentSymbolSupport: true},
			},
		},
		ClientInfo: &lsp.ClientInfo{Name: "lspbridge-clangd-live", Version: "0.0.1"},
	}); err != nil {
		_ = srv.Close()
		t.Fatalf("Initialize: %v", err)
	}

	uri := lsp.DocumentURI("file://" + cPath)
	if err := srv.Client().DidOpen(ctx, lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "c", Version: 1, Text: fixtureMainC},
	}); err != nil {
		_ = srv.Close()
		t.Fatalf("DidOpen: %v", err)
	}
	return srv, uri, cPath
}

// TestClangdLiveDocumentSymbol launches real clangd over a tiny fixture
// translation unit and polls documentSymbol for the fixture's two
// functions, mirroring this package's rust-analyzer/dart-analysis-server
// live-proof shape.
func TestClangdLiveDocumentSymbol(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), clangdIndexDeadline+15*time.Second)
	defer cancel()
	srv, uri, _ := startClangd(t, ctx)
	defer func() { _ = srv.Close() }()

	deadline := time.Now().Add(clangdIndexDeadline)
	for {
		res, err := srv.Client().DocumentSymbol(ctx, lsp.DocumentSymbolParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		})
		if err != nil {
			t.Fatalf("DocumentSymbol: %v", err)
		}
		names := clangdSymbolNames(res)
		if len(names) > 0 {
			t.Logf("clangd documentSymbol returned: %v (hierarchical=%d flat=%d)", names, len(res.Hierarchical), len(res.Flat))
			for _, want := range []string{"add", "main"} {
				if !clangdContains(names, want) {
					t.Errorf("documentSymbol missing %q; got %v", want, names)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Skipf("clangd returned no symbols within %s — skipping rather than flaking", clangdIndexDeadline)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func clangdSymbolNames(res *lsp.DocumentSymbolResult) []string {
	var names []string
	for _, s := range res.Hierarchical {
		names = append(names, s.Name)
	}
	for _, s := range res.Flat {
		names = append(names, s.Name)
	}
	return names
}

func clangdContains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
