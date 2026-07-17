package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// fakeLSPBin is the path to the freshly built fakelsp helper server.
var fakeLSPBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "lspbridge-fakelsp")
	if err != nil {
		panic(err)
	}
	bin := filepath.Join(dir, "fakelsp")
	build := exec.Command("go", "build", "-o", bin, "github.com/davidwalter0/lspbridge/cmd/fakelsp")
	if out, err := build.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		panic("build fakelsp: " + err.Error() + "\n" + string(out))
	}
	fakeLSPBin = bin

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestLaunchLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	srv, err := Launch(ctx, Spec{Command: fakeLSPBin}, nil)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	res, err := srv.Client().Initialize(ctx, lsp.InitializeParams{RootURI: "file:///p"})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if res.ServerInfo == nil || res.ServerInfo.Name != "fakelsp" {
		t.Fatalf("serverInfo = %+v", res.ServerInfo)
	}

	if err := srv.Client().DidOpen(ctx, lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI: "file:///p/a.py", LanguageID: "python", Version: 1, Text: "x = 1\n",
		},
	}); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}

	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestLaunchEmptyCommand(t *testing.T) {
	if _, err := Launch(context.Background(), Spec{}, nil); err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestLaunchMissingBinary(t *testing.T) {
	_, err := Launch(context.Background(), Spec{Command: filepath.Join(t.TempDir(), "does-not-exist")}, nil)
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestCloseForceTerminates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	srv, err := Launch(ctx, Spec{Command: fakeLSPBin}, nil)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if _, err := srv.Client().Initialize(ctx, lsp.InitializeParams{RootURI: "file:///p"}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// Close without an orderly shutdown must not hang or error.
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
