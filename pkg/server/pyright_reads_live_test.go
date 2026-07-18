package server_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/lspbridge/pkg/server"
	"github.com/davidwalter0/lspbridge/pkg/wsedit"
)

// The fixture: one symbol (greet) declared once and used twice, so references
// finds both uses and rename produces a multi-edit WorkspaceEdit.
const fixturePy = `def greet(name):
    return "hi " + name


def main():
    print(greet("a"))
    print(greet("b"))
`

// greet's identifier position (0-indexed, UTF-16): line 0, inside "greet".
var greetPos = lsp.Position{Line: 0, Character: 6}

// startPyright launches real pyright over a fixture project and completes the
// LSP handshake, advertising the read (hierarchical documentSymbol) and write
// (workspaceEdit WITHOUT documentChanges → the "changes" map form wsedit reads)
// capabilities. It skips the test when pyright is not installed.
func startPyright(t *testing.T, ctx context.Context) (*server.Server, lsp.DocumentURI, string) {
	t.Helper()
	if _, err := exec.LookPath(server.PyrightCommand); err != nil {
		t.Skipf("pyright not installed (%v); skipping live proof", err)
	}
	dir := t.TempDir()
	pyPath := filepath.Join(dir, "mod.py")
	if err := os.WriteFile(pyPath, []byte(fixturePy), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	spec, err := server.PyrightSpec(dir)
	if err != nil {
		t.Fatalf("PyrightSpec: %v", err)
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
		ClientInfo: &lsp.ClientInfo{Name: "lspbridge-reads-live", Version: "0.0.1"},
	}); err != nil {
		_ = srv.Close()
		t.Fatalf("Initialize: %v", err)
	}

	uri := lsp.DocumentURI("file://" + pyPath)
	if err := srv.Client().DidOpen(ctx, lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "python", Version: 1, Text: fixturePy},
	}); err != nil {
		_ = srv.Close()
		t.Fatalf("DidOpen: %v", err)
	}
	return srv, uri, pyPath
}

// waitForSymbols polls documentSymbol until pyright has analyzed the file
// (returns at least one symbol) or the deadline passes.
func waitForSymbols(t *testing.T, ctx context.Context, srv *server.Server, uri lsp.DocumentURI) *lsp.DocumentSymbolResult {
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
			t.Fatal("pyright returned no symbols before deadline")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func symbolNames(res *lsp.DocumentSymbolResult) []string {
	var names []string
	for _, s := range res.Hierarchical {
		names = append(names, s.Name)
	}
	for _, s := range res.Flat {
		names = append(names, s.Name)
	}
	return names
}

func TestPyrightLiveDocumentSymbol(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv, uri, _ := startPyright(t, ctx)
	defer func() { _ = srv.Close() }()

	res := waitForSymbols(t, ctx, srv, uri)
	names := symbolNames(res)
	t.Logf("documentSymbol returned: %v (hierarchical=%d flat=%d)", names, len(res.Hierarchical), len(res.Flat))
	for _, want := range []string{"greet", "main"} {
		if !contains(names, want) {
			t.Errorf("documentSymbol missing %q; got %v", want, names)
		}
	}
}

func TestPyrightLiveReferences(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv, uri, _ := startPyright(t, ctx)
	defer func() { _ = srv.Close() }()

	waitForSymbols(t, ctx, srv, uri) // ensure analysis is warm

	locs, err := srv.Client().References(ctx, lsp.ReferenceParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     greetPos,
		Context:      lsp.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	var callSiteLines []int
	for _, l := range locs {
		callSiteLines = append(callSiteLines, l.Range.Start.Line)
	}
	t.Logf("references to greet at lines: %v", callSiteLines)
	// Both call sites (lines 5 and 6) must be found.
	if !containsInt(callSiteLines, 5) || !containsInt(callSiteLines, 6) {
		t.Fatalf("expected references at both use lines 5 and 6; got %v", callSiteLines)
	}
}

// TestPyrightLiveRenameWriteLoop is the first LIVE producer feeding the write
// path: rename via pyright → WorkspaceEdit → wsedit.FromWorkspaceEdit →
// FileEdit.Apply → the renamed source. It proves the whole read-to-write seam
// against a real language server.
func TestPyrightLiveRenameWriteLoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv, uri, pyPath := startPyright(t, ctx)
	defer func() { _ = srv.Close() }()

	waitForSymbols(t, ctx, srv, uri) // ensure analysis is warm

	we, err := srv.Client().Rename(ctx, lsp.RenameParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     greetPos,
		NewName:      "welcome",
	})
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if we == nil || len(we.Changes) == 0 {
		t.Fatalf("rename produced no WorkspaceEdit changes: %+v", we)
	}
	t.Logf("pyright rename produced %d edit(s) across %d file(s)", countEdits(we), len(we.Changes))

	// Feed the pure WorkspaceEdit through the neutral byte-offset seam.
	plan, err := wsedit.FromWorkspaceEdit(*we, map[lsp.DocumentURI][]byte{uri: []byte(fixturePy)})
	if err != nil {
		t.Fatalf("FromWorkspaceEdit: %v", err)
	}
	if plan.Schema != wsedit.SchemaV1 {
		t.Errorf("plan.Schema = %q, want %q", plan.Schema, wsedit.SchemaV1)
	}

	var fe *wsedit.FileEdit
	for i := range plan.Files {
		if plan.Files[i].Path == pyPath {
			fe = &plan.Files[i]
		}
	}
	if fe == nil {
		t.Fatalf("plan has no FileEdit for %s; files=%+v", pyPath, plan.Files)
	}

	out, err := fe.Apply([]byte(fixturePy))
	if err != nil {
		t.Fatalf("FileEdit.Apply: %v", err)
	}
	got := string(out)
	t.Logf("renamed source:\n%s", got)

	if strings.Contains(got, "greet") {
		t.Errorf("renamed source still contains 'greet':\n%s", got)
	}
	for _, want := range []string{"def welcome(name):", `welcome("a")`, `welcome("b")`} {
		if !strings.Contains(got, want) {
			t.Errorf("renamed source missing %q:\n%s", want, got)
		}
	}
}

func countEdits(we *lsp.WorkspaceEdit) int {
	n := 0
	for _, edits := range we.Changes {
		n += len(edits)
	}
	return n
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
