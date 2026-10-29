package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/lspbridge/pkg/lspserver"
)

// kubeconformDiags builds the real gts-lsp binary, opens a Deployment with
// `replicas: "three"` under the given XDG_CACHE_HOME, and returns the first
// published diagnostic set that carries a kubeconform entry. No network.
func kubeconformDiags(t *testing.T, bin, cacheHome string) []lsp.Diagnostic {
	t.Helper()
	fixture, err := filepath.Abs("../../validate/testdata/deploy_bad.yaml")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cacheHome)
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	uri := lspserver.PathToURI(fixture)
	got := make(chan []lsp.Diagnostic, 16)
	conn := jsonrpc.NewConn(jsonrpc.Join(out, in), jsonrpc.HandlerFunc(
		func(_ context.Context, r *jsonrpc.Request) (any, error) {
			var p lsp.PublishDiagnosticsParams
			if r.Method == "textDocument/publishDiagnostics" && json.Unmarshal(r.Params, &p) == nil && p.URI == uri {
				got <- p.Diagnostics
			}
			return nil, nil
		}))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var res lsp.InitializeResult
	if err := conn.Call(ctx, "initialize", lsp.InitializeParams{ProcessID: os.Getpid(), RootURI: "file:///"}, &res); err != nil {
		t.Fatal(err)
	}
	_ = conn.Notify(ctx, "initialized", struct{}{})
	_ = conn.Notify(ctx, "textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "yaml", Version: 1, Text: string(src)}})
	deadline := time.After(40 * time.Second)
	for {
		select {
		case ds := <-got:
			for _, d := range ds {
				if d.Source == "kubeconform" {
					return ds
				}
			}
		case <-deadline:
			t.Fatal("no kubeconform diagnostic published")
		}
	}
}

func TestKubeconformCacheStatesEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("kubeconform"); err != nil {
		t.Skip("kubeconform is not installed (genuinely absent)")
	}
	bin := filepath.Join(t.TempDir(), "gts-lsp")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if outb, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, outb)
	}

	t.Run("populated cache gives an Error on the replicas line", func(t *testing.T) {
		cache := t.TempDir()
		if err := os.CopyFS(filepath.Join(cache, "gts-lsp", "schemas"), os.DirFS("../../validate/testdata/schemas")); err != nil {
			t.Fatal(err)
		}
		for _, d := range kubeconformDiags(t, bin, cache) {
			if d.Source == "kubeconform" && d.Severity == lsp.SeverityError &&
				d.Range.Start.Line == 5 && strings.Contains(d.Message, "/spec/replicas") {
				return
			}
		}
		t.Fatal("no kubeconform Error on line 5 (replicas)")
	})

	t.Run("empty cache gives the Information no-cached-schema diagnostic", func(t *testing.T) {
		for _, d := range kubeconformDiags(t, bin, t.TempDir()) {
			if d.Source != "kubeconform" {
				continue
			}
			if d.Severity != lsp.SeverityInformation || d.Range.Start.Line != 0 ||
				!strings.Contains(d.Message, "no cached schema for apps/v1 Deployment") ||
				!strings.Contains(d.Message, "gts-lsp schemas sync") {
				t.Fatalf("unexpected kubeconform diagnostic: %+v", d)
			}
			return
		}
		t.Fatal("no kubeconform diagnostic")
	})
}
