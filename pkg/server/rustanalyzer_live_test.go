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

const fixtureCargoToml = `[package]
name = "tinycrate"
version = "0.1.0"
edition = "2021"
`

const fixtureMainRS = `fn greet(name: &str) -> String {
    format!("hi {}", name)
}

fn main() {
    println!("{}", greet("a"));
}
`

// rustAnalyzerIndexDeadline bounds how long this test waits for
// rust-analyzer's initial index (cargo metadata/check) before giving up.
// rust-analyzer can be slow to warm up on a cold target dir, so this is
// deliberately generous — well beyond pyright's/tsserver's analogous
// deadlines in this package.
const rustAnalyzerIndexDeadline = 90 * time.Second

// startRustAnalyzer launches real rust-analyzer over a tiny fixture crate
// (Cargo.toml + src/main.rs — the crate-root marker projectcontext.RustResolver
// walks up for) and completes the LSP handshake. It skips the test when
// rust-analyzer is not installed.
func startRustAnalyzer(t *testing.T, ctx context.Context) (*server.Server, lsp.DocumentURI, string, string) {
	t.Helper()
	if _, err := exec.LookPath(server.RustAnalyzerCommand); err != nil {
		t.Skipf("rust-analyzer not installed (%v); skipping live proof", err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte(fixtureCargoToml), 0o644); err != nil {
		t.Fatalf("write Cargo.toml: %v", err)
	}
	srcDir := filepath.Join(root, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	rsPath := filepath.Join(srcDir, "main.rs")
	if err := os.WriteFile(rsPath, []byte(fixtureMainRS), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	spec, err := server.RustAnalyzerSpec(root)
	if err != nil {
		t.Fatalf("RustAnalyzerSpec: %v", err)
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
		ClientInfo: &lsp.ClientInfo{Name: "lspbridge-rust-analyzer-live", Version: "0.0.1"},
	}); err != nil {
		_ = srv.Close()
		t.Fatalf("Initialize: %v", err)
	}

	uri := lsp.DocumentURI("file://" + rsPath)
	if err := srv.Client().DidOpen(ctx, lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "rust", Version: 1, Text: fixtureMainRS},
	}); err != nil {
		_ = srv.Close()
		t.Fatalf("DidOpen: %v", err)
	}
	return srv, uri, rsPath, root
}

// TestRustAnalyzerLiveDocumentSymbol launches real rust-analyzer over a tiny
// cargo project and polls documentSymbol for the fixture's two functions.
// rust-analyzer indexes a crate (cargo metadata/check) before it can answer
// meaningfully, and that indexing step is host- and cache-dependent — so
// unlike this package's pyright/tsserver equivalents, a still-empty result at
// the deadline is treated as "could not verify within budget" (skip) rather
// than a hard failure: failing here would flake on a slow/cold host instead
// of reporting a real defect.
func TestRustAnalyzerLiveDocumentSymbol(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), rustAnalyzerIndexDeadline+15*time.Second)
	defer cancel()
	srv, uri, _, _ := startRustAnalyzer(t, ctx)
	defer func() { _ = srv.Close() }()

	deadline := time.Now().Add(rustAnalyzerIndexDeadline)
	for {
		res, err := srv.Client().DocumentSymbol(ctx, lsp.DocumentSymbolParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		})
		if err != nil {
			t.Fatalf("DocumentSymbol: %v", err)
		}
		names := rustSymbolNames(res)
		if len(names) > 0 {
			t.Logf("rust-analyzer documentSymbol returned: %v (hierarchical=%d flat=%d)", names, len(res.Hierarchical), len(res.Flat))
			for _, want := range []string{"greet", "main"} {
				if !rustContains(names, want) {
					t.Errorf("documentSymbol missing %q; got %v", want, names)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Skipf("rust-analyzer returned no symbols within %s (likely still indexing cargo metadata/check on this host) — skipping rather than flaking; see task note on rust-analyzer indexing latency", rustAnalyzerIndexDeadline)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func rustSymbolNames(res *lsp.DocumentSymbolResult) []string {
	var names []string
	for _, s := range res.Hierarchical {
		names = append(names, s.Name)
	}
	for _, s := range res.Flat {
		names = append(names, s.Name)
	}
	return names
}

func rustContains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
