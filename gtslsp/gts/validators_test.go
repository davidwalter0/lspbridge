package gts

import (
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/gtslsp/validate"
	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/lspbridge/pkg/lspserver"
)

type fakeValidator struct {
	name      string
	available bool
	runs      atomic.Int32
	diag      lsp.Diagnostic
}

func (f *fakeValidator) Name() string                     { return f.name }
func (f *fakeValidator) Applies(d validate.Document) bool { return d.Language == "yaml" }
func (f *fakeValidator) Available() (bool, string)        { return f.available, "install " + f.name }
func (f *fakeValidator) Validate(_ context.Context, _ validate.Document) ([]lsp.Diagnostic, error) {
	f.runs.Add(1)
	return []lsp.Diagnostic{f.diag}, nil
}

func TestValidatorsMergeDebounceAndUnavailableHint(t *testing.T) {
	good := &fakeValidator{name: "fakelint", available: true,
		diag: lsp.Diagnostic{Source: "fakelint", Message: "bad style", Severity: lsp.SeverityWarning}}
	missing := &fakeValidator{name: "nolint", available: false}
	srv := New(Config{Validators: []validate.Validator{good, missing}})

	cs, ss := net.Pipe()
	diags := make(chan []lsp.Diagnostic, 16)
	msgs := make(chan string, 16)
	client := jsonrpc.NewConn(cs, jsonrpc.HandlerFunc(func(_ context.Context, r *jsonrpc.Request) (any, error) {
		switch r.Method {
		case "textDocument/publishDiagnostics":
			var p lsp.PublishDiagnosticsParams
			_ = json.Unmarshal(r.Params, &p)
			diags <- p.Diagnostics
		case "window/showMessage":
			var p lsp.ShowMessageParams
			_ = json.Unmarshal(r.Params, &p)
			msgs <- p.Message
		}
		return nil, nil
	}))
	defer func() { _ = client.Close() }()
	go lspserver.Serve(context.Background(), ss, srv.Options())

	ctx := context.Background()
	if err := client.Call(ctx, "initialize", lsp.InitializeParams{}, nil); err != nil {
		t.Fatal(err)
	}
	open := func(uri string) {
		_ = client.Notify(ctx, "textDocument/didOpen", lsp.DidOpenTextDocumentParams{
			TextDocument: lsp.TextDocumentItem{URI: lsp.DocumentURI(uri), Version: 1, Text: "a: b: c\n"}})
	}
	open("file:///w/one.yaml")
	first := <-diags // syntax diagnostics, published immediately
	if len(first) == 0 || first[0].Source != "gts" {
		t.Fatalf("first publish = %+v", first)
	}
	var merged []lsp.Diagnostic
	select {
	case merged = <-diags:
	case <-time.After(10 * time.Second):
		t.Fatal("validator diagnostics never published")
	}
	sources := map[string]bool{}
	for _, d := range merged {
		sources[d.Source] = true
	}
	if !sources["gts"] || !sources["fakelint"] {
		t.Errorf("merged diagnostics lack a source: %+v", merged)
	}
	select {
	case m := <-msgs:
		if m == "" || !contains(m, "nolint") || !contains(m, "install nolint") {
			t.Errorf("hint message = %q", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no showMessage for the unavailable validator")
	}
	// A second document must not repeat the hint.
	open("file:///w/two.yaml")
	deadline := time.After(2 * time.Second)
	for done := false; !done; {
		select {
		case <-diags:
		case m := <-msgs:
			t.Errorf("hint repeated: %q", m)
		case <-deadline:
			done = true
		}
	}
	if got := good.runs.Load(); got != 2 {
		t.Errorf("fakelint ran %d times for 2 opens", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
