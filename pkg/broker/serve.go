package broker

import (
	"context"
	"encoding/json"
	"net"
	"sync"

	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
)

// Handler adapts a [Broker] to [jsonrpc.Handler], dispatching the broker/*
// wire methods. One Handler is shared across all accepted connections (the
// Broker is concurrency-safe). onShutdown, if non-nil, is invoked when a client
// calls "broker/shutdown" — the daemon wires it to cancel its serve context.
type Handler struct {
	b        *Broker
	onceStop sync.Once
	stop     func()
}

// NewHandler returns a Handler over b. onShutdown may be nil.
func NewHandler(b *Broker, onShutdown func()) *Handler {
	return &Handler{b: b, stop: onShutdown}
}

// Handle implements [jsonrpc.Handler].
func (h *Handler) Handle(ctx context.Context, req *jsonrpc.Request) (any, error) {
	switch req.Method {
	case MethodQuery:
		var qr QueryRequest
		if err := json.Unmarshal(req.Params, &qr); err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "broker/query: " + err.Error()}
		}
		raw, err := h.b.Query(ctx, qr)
		if err != nil {
			return nil, err // Conn wraps a plain error as CodeInternalError
		}
		return raw, nil
	case MethodStatus:
		return h.b.Status(), nil
	case MethodShutdown:
		if h.stop != nil {
			// Defer the stop so this request's response is written before the
			// serve context is canceled (canceling it tears down this very
			// connection). onceStop keeps it single-shot across connections.
			go h.onceStop.Do(h.stop)
		}
		return ShutdownResponse{OK: true}, nil
	default:
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "broker: method not found: " + req.Method}
	}
}

// Serve accepts connections on ln and serves the broker protocol on each until
// ctx is canceled, then drains in-flight connections and returns. It also runs
// the idle-reaper for the broker's lifetime. Serve owns ln: it closes ln on
// return (which is how a blocked Accept is unblocked on cancel). It does NOT
// call [Broker.ShutdownAll] — the daemon does that after Serve returns so the
// exact ordering (drain, then reap sessions, then remove the socket) stays in
// the daemon.
func Serve(ctx context.Context, ln net.Listener, h *Handler) error {
	go h.b.reapLoop(ctx)

	// Close the listener when ctx is canceled to unblock Accept.
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = ln.Close()
		case <-stopped:
		}
	}()
	defer close(stopped)

	var conns sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			// A cancel-driven close is a clean stop; anything else is real.
			select {
			case <-ctx.Done():
				conns.Wait()
				return nil
			default:
				conns.Wait()
				return err
			}
		}
		conns.Add(1)
		go func() {
			defer conns.Done()
			serveConn(ctx, conn, h)
		}()
	}
}

// serveConn wraps one accepted connection in a jsonrpc.Conn with the shared
// handler and blocks until the peer disconnects or the broker is canceled.
func serveConn(ctx context.Context, conn net.Conn, h *Handler) {
	c := jsonrpc.NewConn(conn, h)
	defer func() { _ = c.Close() }()
	select {
	case <-ctx.Done():
	case <-c.Done():
	}
}
