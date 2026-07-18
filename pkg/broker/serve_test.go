package broker

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// newPipeClient wires a broker.Client to a Handler over an in-memory pipe.
func newPipeClient(t *testing.T, b *Broker, onShutdown func()) *Client {
	t.Helper()
	cconn, sconn := net.Pipe()
	h := NewHandler(b, onShutdown)
	serverConn := jsonrpc.NewConn(sconn, h)
	clientConn := jsonrpc.NewConn(cconn, nil)
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return NewClient(clientConn)
}

func TestHandlerQueryStatusOverPipe(t *testing.T) {
	b, _, _ := newFakeBroker(t)
	root := b.resolver.(fakeResolver).root
	var stopped atomic.Bool
	cli := newPipeClient(t, b, func() { stopped.Store(true) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	raw, err := cli.Query(ctx, docSymbolQuery(root))
	if err != nil {
		t.Fatalf("client Query: %v", err)
	}
	var syms []lsp.DocumentSymbol
	if err := json.Unmarshal(raw, &syms); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if len(syms) != 1 || syms[0].Name != "cannedSymbol" {
		t.Fatalf("result = %+v", syms)
	}

	st, err := cli.Status(ctx)
	if err != nil {
		t.Fatalf("client Status: %v", err)
	}
	if len(st.Sessions) != 1 || st.Sessions[0].Language != "python" {
		t.Fatalf("status = %+v", st)
	}

	if err := cli.Shutdown(ctx); err != nil {
		t.Fatalf("client Shutdown: %v", err)
	}
	// The deferred stop runs in a goroutine; give it a beat to fire.
	deadline := time.After(2 * time.Second)
	for !stopped.Load() {
		select {
		case <-deadline:
			t.Fatal("onShutdown never fired")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestHandlerUnknownMethod(t *testing.T) {
	b, _, _ := newFakeBroker(t)
	cli := newPipeClient(t, b, nil)
	err := cli.conn.Call(context.Background(), "broker/nope", nil, nil)
	if err == nil {
		t.Fatal("expected method-not-found error")
	}
	var rpcErr *jsonrpc.Error
	if !asJSONRPCError(err, &rpcErr) || rpcErr.Code != jsonrpc.CodeMethodNotFound {
		t.Fatalf("error = %v, want method-not-found", err)
	}
}

func TestServeOverUnixSocket(t *testing.T) {
	b, _, _ := newFakeBroker(t)
	root := b.resolver.(fakeResolver).root
	sockPath := filepath.Join(t.TempDir(), "broker.sock")

	ln, err := Listen(sockPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := NewHandler(b, cancel)

	served := make(chan error, 1)
	go func() { served <- Serve(ctx, ln, h) }()

	cli, err := Dial(sockPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	qctx, qcancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer qcancel()

	raw, err := cli.Query(qctx, docSymbolQuery(root))
	if err != nil {
		t.Fatalf("Query over socket: %v", err)
	}
	var syms []lsp.DocumentSymbol
	if err := json.Unmarshal(raw, &syms); err != nil || len(syms) != 1 || syms[0].Name != "cannedSymbol" {
		t.Fatalf("socket query result = %s (%v)", raw, err)
	}

	st, err := cli.Status(qctx)
	if err != nil || len(st.Sessions) != 1 {
		t.Fatalf("socket status = %+v (%v)", st, err)
	}

	// broker/shutdown cancels the serve context; Serve must drain and return.
	_ = cli.Shutdown(qctx)
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after shutdown")
	}
	b.ShutdownAll()
	_ = os.Remove(sockPath)
}

func TestClientDialError(t *testing.T) {
	if _, err := Dial(filepath.Join(t.TempDir(), "no-such.sock")); err == nil {
		t.Fatal("Dial to a nonexistent socket should error")
	}
}

func TestClientCallsAfterClose(t *testing.T) {
	b, _, _ := newFakeBroker(t)
	cli := newPipeClient(t, b, nil)
	_ = cli.Close()
	ctx := context.Background()
	if _, err := cli.Query(ctx, QueryRequest{File: "/x/a.py", Method: "m"}); err == nil {
		t.Error("Query after Close should error")
	}
	if _, err := cli.Status(ctx); err == nil {
		t.Error("Status after Close should error")
	}
	if err := cli.Shutdown(ctx); err == nil {
		t.Error("Shutdown after Close should error")
	}
}

// asJSONRPCError is a tiny errors.As shim kept local to avoid importing errors
// just for one call in a test.
func asJSONRPCError(err error, target **jsonrpc.Error) bool {
	e, ok := err.(*jsonrpc.Error)
	if ok {
		*target = e
	}
	return ok
}
