package broker_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/broker"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/lspbridge/pkg/server"
)

const liveTS = `function greet(name: string): string {
    return "hi " + name;
}

function main(): void {
    console.log(greet("a"));
    console.log(greet("b"));
}
`

const liveTSConfig = `{
  "compilerOptions": {
    "target": "ES2020",
    "module": "commonjs",
    "strict": true
  }
}
`

const livePackageJSON = `{
  "name": "lspbridge-broker-ts-fixture",
  "version": "1.0.0",
  "private": true
}
`

// TestBrokerLiveTSServerNodeProjectContext is the Node-ProjectContext
// end-to-end proof (mgmt c8d915b0 part (c) / f51cf319 remaining item (2)): a
// real client dials the broker over a real unix socket with an ALL-DEFAULT
// Config (projectcontext.DefaultChain + DefaultSpecFor — no test-only
// wiring), the broker's resolver discovers the project root from the
// fixture's package.json + tsconfig.json markers exactly as a real Node
// project would, activates typescript-language-server for it, and
// documentSymbol comes back through that one warm session. Skipped when
// typescript-language-server is not installed.
func TestBrokerLiveTSServerNodeProjectContext(t *testing.T) {
	if _, err := exec.LookPath(server.TypeScriptLanguageServerCommand); err != nil {
		t.Skipf("typescript-language-server not installed (%v); skipping broker live proof", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(livePackageJSON), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(liveTSConfig), 0o644); err != nil {
		t.Fatalf("write tsconfig.json: %v", err)
	}
	tsPath := filepath.Join(dir, "mod.ts")
	if err := os.WriteFile(tsPath, []byte(liveTS), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	uri := lsp.DocumentURI("file://" + tsPath)

	// Default config: real projectcontext.DefaultChain (TypeScriptResolver
	// walks up to the fixture's tsconfig.json) + DefaultSpecFor (typescript ->
	// typescript-language-server). Nothing here is test-only wiring — this is
	// exactly what a real Node project hitting the broker would exercise.
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
	text := liveTS

	names := pollBrokerTSSymbols(t, qctx, cli, tsPath, uri, &text)
	t.Logf("broker documentSymbol (typescript): %v", names)
	if !hasName(names, "greet") || !hasName(names, "main") {
		t.Fatalf("broker documentSymbol missing greet/main: %v", names)
	}

	// Status over the socket shows the one warm session, resolved to
	// "typescript" purely from the tsconfig.json/package.json markers — the
	// Node ProjectContext seam working end-to-end through the broker.
	st, err := cli.Status(qctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(st.Sessions) != 1 || st.Sessions[0].Language != "typescript" || st.Sessions[0].Root != dir {
		t.Fatalf("status = %+v", st)
	}
}

func pollBrokerTSSymbols(t *testing.T, ctx context.Context, cli *broker.Client, tsPath string, uri lsp.DocumentURI, text *string) []string {
	t.Helper()
	params, _ := json.Marshal(lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
	})
	deadline := time.Now().Add(45 * time.Second)
	for {
		raw, err := cli.Query(ctx, broker.QueryRequest{
			File:   tsPath,
			Method: "textDocument/documentSymbol",
			Params: params,
			Text:   text,
		})
		if err != nil {
			t.Fatalf("documentSymbol query: %v", err)
		}
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
