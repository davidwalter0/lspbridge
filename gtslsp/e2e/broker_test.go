package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/broker"
	"github.com/davidwalter0/lspbridge/pkg/gtsfallback"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// TestBrokerFallsBackToGtsLsp runs the real broker (in process, no shared
// socket) with the gts-lsp fallback and the real binary: a YAML file, which no
// dedicated resolver claims, is served by gts-lsp, and the session reports the
// server's identity and capabilities.
func TestBrokerFallsBackToGtsLsp(t *testing.T) {
	t.Setenv("PATH", filepath.Dir(binPath)+string(os.PathListSeparator)+os.Getenv("PATH"))
	fb := &gtsfallback.Fallback{}
	if !fb.Available() {
		t.Fatal("gts-lsp not found on PATH")
	}
	b := broker.New(broker.Config{
		Resolver:    fb.Chain(),
		SpecFor:     fb.SpecFor(broker.DefaultSpecFor),
		IdleTimeout: time.Minute,
	})
	t.Cleanup(b.ShutdownAll)

	file, _ := filepath.Abs(fixture("yaml", "clean"))
	params, _ := json.Marshal(lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: lsp.DocumentURI("file://" + file)},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	raw, err := b.Query(ctx, broker.QueryRequest{File: file, Method: "textDocument/documentSymbol", Params: params})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var syms []lsp.DocumentSymbol
	if err := json.Unmarshal(raw, &syms); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	got := strings.Join(flat(syms, ""), ",")
	for _, want := range []string{"name", "image/repository", "ports", "env/LOG_LEVEL"} {
		if !strings.Contains(got, want) {
			t.Errorf("symbol %q missing from %s", want, got)
		}
	}
	st := b.Status()
	if len(st.Sessions) != 1 {
		t.Fatalf("sessions = %d", len(st.Sessions))
	}
	s := st.Sessions[0]
	if s.Language != "yaml" || s.ServerInfo == nil || s.ServerInfo.Name != "gts-lsp" {
		t.Errorf("session = %+v", s)
	}
	if !strings.Contains(string(s.Capabilities), `"documentSymbolProvider":true`) {
		t.Errorf("capabilities = %s", s.Capabilities)
	}
}
