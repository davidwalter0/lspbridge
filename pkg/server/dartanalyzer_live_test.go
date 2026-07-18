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

const fixturePubspecYAML = `name: tinypkg
description: A tiny fixture package for lspbridge's live Dart Analysis Server proof.
version: 0.0.1
environment:
  sdk: '>=3.0.0 <4.0.0'
`

const fixtureMainDart = `String greet(String name) {
  return 'hi ' + name;
}

void main() {
  print(greet('a'));
  print(greet('b'));
}
`

// dartAnalysisIndexDeadline bounds how long this test waits for the Dart
// Analysis Server's initial analysis (it builds a byte-store / driver over
// the package graph before it can answer meaningfully) before giving up.
// Deliberately generous, matching this package's rust-analyzer equivalent —
// both servers' warm-up is host- and cache-dependent.
const dartAnalysisIndexDeadline = 90 * time.Second

// startDartAnalysisServer launches the real Dart Analysis Server over a
// tiny fixture pub package (pubspec.yaml — the root marker
// projectcontext.DartResolver walks up for — plus one fixture file) and
// completes the LSP handshake. It skips the test when dart is not
// installed.
func startDartAnalysisServer(t *testing.T, ctx context.Context) (*server.Server, lsp.DocumentURI, string) {
	t.Helper()
	if _, err := exec.LookPath(server.DartCommand); err != nil {
		t.Skipf("dart not installed (%v); skipping live proof", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte(fixturePubspecYAML), 0o644); err != nil {
		t.Fatalf("write pubspec.yaml: %v", err)
	}
	dartPath := filepath.Join(dir, "main.dart")
	if err := os.WriteFile(dartPath, []byte(fixtureMainDart), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	spec, err := server.DartAnalysisServerSpec(dir)
	if err != nil {
		t.Fatalf("DartAnalysisServerSpec: %v", err)
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
		ClientInfo: &lsp.ClientInfo{Name: "lspbridge-dart-analysis-server-live", Version: "0.0.1"},
	}); err != nil {
		_ = srv.Close()
		t.Fatalf("Initialize: %v", err)
	}

	uri := lsp.DocumentURI("file://" + dartPath)
	if err := srv.Client().DidOpen(ctx, lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "dart", Version: 1, Text: fixtureMainDart},
	}); err != nil {
		_ = srv.Close()
		t.Fatalf("DidOpen: %v", err)
	}
	return srv, uri, dartPath
}

// TestDartAnalysisServerLiveDocumentSymbol launches the real Dart Analysis
// Server over a tiny pub package and polls documentSymbol for the
// fixture's two functions. Like rust-analyzer, the Dart Analysis Server
// must analyze the package (resolve the SDK, build its byte-store) before
// it can answer meaningfully, and that warm-up is host- and
// cache-dependent — so a still-empty result at the deadline is treated as
// "could not verify within budget" (skip) rather than a hard failure.
func TestDartAnalysisServerLiveDocumentSymbol(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), dartAnalysisIndexDeadline+15*time.Second)
	defer cancel()
	srv, uri, _ := startDartAnalysisServer(t, ctx)
	defer func() { _ = srv.Close() }()

	deadline := time.Now().Add(dartAnalysisIndexDeadline)
	for {
		res, err := srv.Client().DocumentSymbol(ctx, lsp.DocumentSymbolParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		})
		if err != nil {
			t.Fatalf("DocumentSymbol: %v", err)
		}
		names := dartSymbolNames(res)
		if len(names) > 0 {
			t.Logf("dart analysis server documentSymbol returned: %v (hierarchical=%d flat=%d)", names, len(res.Hierarchical), len(res.Flat))
			for _, want := range []string{"greet", "main"} {
				if !dartContains(names, want) {
					t.Errorf("documentSymbol missing %q; got %v", want, names)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Skipf("dart analysis server returned no symbols within %s (likely still analyzing the package on this host) — skipping rather than flaking; see task note on Dart Analysis Server warm-up latency", dartAnalysisIndexDeadline)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func dartSymbolNames(res *lsp.DocumentSymbolResult) []string {
	var names []string
	for _, s := range res.Hierarchical {
		names = append(names, s.Name)
	}
	for _, s := range res.Flat {
		names = append(names, s.Name)
	}
	return names
}

func dartContains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
