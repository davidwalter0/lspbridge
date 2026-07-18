package broker_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/broker"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/lspbridge/pkg/wsedit"
)

const livePy = `def greet(name):
    return "hi " + name


def main():
    print(greet("a"))
    print(greet("b"))
`

// TestBrokerLivePyrightEndToEnd is the keystone end-to-end proof: a real client
// dials the broker over a real unix socket, the broker lazily socket-activates
// pyright for the file's project, and both a READ (documentSymbol) and the
// WRITE loop (rename → WorkspaceEdit → wsedit → Apply) succeed over that one
// warm session. Skipped when pyright is not installed.
func TestBrokerLivePyrightEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("pyright-langserver"); err != nil {
		t.Skipf("pyright not installed (%v); skipping broker live proof", err)
	}

	dir := t.TempDir()
	pyPath := filepath.Join(dir, "mod.py")
	if err := os.WriteFile(pyPath, []byte(livePy), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	uri := lsp.DocumentURI("file://" + pyPath)

	// Default config: real PythonResolver (falls back to the file's dir as root
	// when no marker exists) + DefaultSpecFor (python → pyright).
	b := broker.New(broker.Config{})
	sockPath := filepath.Join(t.TempDir(), "broker.sock")
	ln, err := broker.Listen(sockPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := broker.NewHandler(b, cancel)
	served := make(chan error, 1)
	go func() { served <- broker.Serve(ctx, ln, h) }()
	defer func() {
		cancel()  // stop Serve (it blocks on ctx.Done)
		<-served  // wait for the accept loop to drain
		b.ShutdownAll()
	}()

	cli, err := broker.Dial(sockPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = cli.Close() }()

	qctx, qcancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer qcancel()
	text := livePy

	// READ: poll documentSymbol through the broker until pyright has analyzed.
	names := pollBrokerSymbols(t, qctx, cli, pyPath, uri, &text)
	t.Logf("broker documentSymbol: %v", names)
	if !hasName(names, "greet") || !hasName(names, "main") {
		t.Fatalf("broker documentSymbol missing greet/main: %v", names)
	}

	// WRITE loop through the broker: rename → WorkspaceEdit → wsedit → Apply.
	renameParams, _ := json.Marshal(lsp.RenameParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 0, Character: 6},
		NewName:      "welcome",
	})
	raw, err := cli.Query(qctx, broker.QueryRequest{
		File:   pyPath,
		Method: "textDocument/rename",
		Params: renameParams,
		Text:   &text,
	})
	if err != nil {
		t.Fatalf("broker rename query: %v", err)
	}
	var we lsp.WorkspaceEdit
	if err := json.Unmarshal(raw, &we); err != nil {
		t.Fatalf("decode WorkspaceEdit %s: %v", raw, err)
	}
	if len(we.Changes) == 0 {
		t.Fatalf("broker rename produced no changes: %s", raw)
	}

	plan, err := wsedit.FromWorkspaceEdit(we, map[lsp.DocumentURI][]byte{uri: []byte(livePy)})
	if err != nil {
		t.Fatalf("FromWorkspaceEdit: %v", err)
	}
	var out []byte
	for i := range plan.Files {
		if plan.Files[i].Path == pyPath {
			out, err = plan.Files[i].Apply([]byte(livePy))
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
		}
	}
	got := string(out)
	t.Logf("broker write-loop renamed source:\n%s", got)
	if strings.Contains(got, "greet") || !strings.Contains(got, "def welcome(name):") {
		t.Fatalf("broker write-loop result wrong:\n%s", got)
	}

	// Status over the socket shows the one warm session.
	st, err := cli.Status(qctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(st.Sessions) != 1 || st.Sessions[0].Language != "python" {
		t.Fatalf("status = %+v", st)
	}
}

func pollBrokerSymbols(t *testing.T, ctx context.Context, cli *broker.Client, pyPath string, uri lsp.DocumentURI, text *string) []string {
	t.Helper()
	params, _ := json.Marshal(lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
	})
	deadline := time.Now().Add(45 * time.Second)
	for {
		raw, err := cli.Query(ctx, broker.QueryRequest{
			File:   pyPath,
			Method: "textDocument/documentSymbol",
			Params: params,
			Text:   text,
		})
		if err != nil {
			// Any error is fatal; an unanalyzed file returns an empty result,
			// not an error, so the retry below is keyed on empty symbols only.
			t.Fatalf("documentSymbol query: %v", err)
		}
		// The broker advertises hierarchicalDocumentSymbolSupport, so pyright
		// replies with DocumentSymbol[]; a flat fallback still yields names.
		var hier []lsp.DocumentSymbol
		var flat []lsp.SymbolInformation
		_ = json.Unmarshal(raw, &hier)
		if len(hier) == 0 {
			_ = json.Unmarshal(raw, &flat)
		}
		names := append(symNames(hier), flatNames(flat)...)
		if len(names) > 0 {
			return names
		}
		if time.Now().After(deadline) {
			t.Fatal("broker documentSymbol returned no symbols before deadline")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func symNames(xs []lsp.DocumentSymbol) []string {
	var n []string
	for _, s := range xs {
		n = append(n, s.Name)
	}
	return n
}

func flatNames(xs []lsp.SymbolInformation) []string {
	var n []string
	for _, s := range xs {
		n = append(n, s.Name)
	}
	return n
}

func hasName(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
