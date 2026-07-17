package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"
)

// pipePair wires two Conns together over an in-memory duplex pipe. clientH
// and serverH are each side's inbound handler.
func pipePair(t *testing.T, clientH, serverH Handler) (client, server *Conn) {
	t.Helper()
	a, b := net.Pipe()
	client = NewConn(a, clientH)
	server = NewConn(b, serverH)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client, server
}

func TestCallResponse(t *testing.T) {
	// Server echoes back {"pong": <params.ping>}.
	server := HandlerFunc(func(_ context.Context, req *Request) (any, error) {
		if req.Method != "ping" {
			return nil, &Error{Code: CodeMethodNotFound, Message: req.Method}
		}
		var p struct {
			Ping string `json:"ping"`
		}
		_ = json.Unmarshal(req.Params, &p)
		return map[string]string{"pong": p.Ping}, nil
	})
	client, _ := pipePair(t, nil, server)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var got struct {
		Pong string `json:"pong"`
	}
	if err := client.Call(ctx, "ping", map[string]string{"ping": "hi"}, &got); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got.Pong != "hi" {
		t.Errorf("pong = %q, want %q", got.Pong, "hi")
	}
}

func TestCallRemoteError(t *testing.T) {
	server := HandlerFunc(func(_ context.Context, req *Request) (any, error) {
		return nil, &Error{Code: CodeMethodNotFound, Message: "no such method: " + req.Method}
	})
	client, _ := pipePair(t, nil, server)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := client.Call(ctx, "missing", nil, nil)
	if err == nil {
		t.Fatal("expected remote error, got nil")
	}
	var rpcErr *Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if rpcErr.Code != CodeMethodNotFound {
		t.Errorf("code = %d, want %d", rpcErr.Code, CodeMethodNotFound)
	}
}

func TestNotifyReachesHandler(t *testing.T) {
	got := make(chan string, 1)
	server := HandlerFunc(func(_ context.Context, req *Request) (any, error) {
		got <- req.Method
		return nil, nil
	})
	client, _ := pipePair(t, nil, server)

	if err := client.Notify(context.Background(), "textDocument/didOpen", struct{}{}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	select {
	case m := <-got:
		if m != "textDocument/didOpen" {
			t.Errorf("method = %q", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification never reached handler")
	}
}

func TestServerToClientRequest(t *testing.T) {
	// The client answers a server-initiated request.
	clientH := HandlerFunc(func(_ context.Context, req *Request) (any, error) {
		if req.Method == "window/showMessageRequest" {
			return map[string]string{"title": "OK"}, nil
		}
		return nil, &Error{Code: CodeMethodNotFound, Message: req.Method}
	})
	_, server := pipePair(t, clientH, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var reply struct {
		Title string `json:"title"`
	}
	if err := server.Call(ctx, "window/showMessageRequest", nil, &reply); err != nil {
		t.Fatalf("server Call: %v", err)
	}
	if reply.Title != "OK" {
		t.Errorf("title = %q, want OK", reply.Title)
	}
}

func TestCallAfterCloseFails(t *testing.T) {
	client, _ := pipePair(t, nil, nil)
	_ = client.Close()

	err := client.Call(context.Background(), "ping", nil, nil)
	if err == nil {
		t.Fatal("expected error after close, got nil")
	}
}

func TestCallContextCancel(t *testing.T) {
	// Server that withholds its reply until cleanup, so the client's Call
	// must unblock on ctx cancellation rather than on a response.
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	server := HandlerFunc(func(_ context.Context, _ *Request) (any, error) {
		<-release
		return nil, nil
	})
	client, _ := pipePair(t, nil, server)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := client.Call(ctx, "hang", nil, nil)
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
}
