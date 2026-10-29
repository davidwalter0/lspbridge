package jsonrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
)

// ErrClosed is returned by pending calls when the connection is closed or
// its read loop terminates.
var ErrClosed = errors.New("jsonrpc: connection closed")

// Handler handles inbound server-to-client requests and notifications. For a
// request (non-nil Request.ID) the returned value is marshaled as the result;
// returning an *Error sends that error verbatim. For a notification the
// return values are ignored. A nil Handler drops notifications and answers
// requests with method-not-found.
type Handler interface {
	Handle(ctx context.Context, req *Request) (result any, err error)
}

// HandlerFunc adapts an ordinary function to [Handler].
type HandlerFunc func(ctx context.Context, req *Request) (any, error)

// Handle implements [Handler].
func (f HandlerFunc) Handle(ctx context.Context, req *Request) (any, error) {
	return f(ctx, req)
}

// Conn is a JSON-RPC 2.0 connection over a framed byte stream. It is safe for
// concurrent use: multiple goroutines may Call and Notify simultaneously.
type Conn struct {
	w io.Writer
	r *bufio.Reader
	c io.Closer

	writeMu sync.Mutex    // serializes frame writes
	seq     atomic.Uint64 // request-ID counter

	mu       sync.Mutex
	pending  map[string]chan *Response
	closed   bool
	closeErr error
	done     chan struct{}

	handler Handler

	inflight map[string]context.CancelFunc // inbound requests by ID; guarded by mu
	baseCtx  context.Context
	baseStop context.CancelFunc
}

// NewConn creates a Conn over rwc and starts its background read loop. The
// caller must Close the Conn to stop the loop and release resources. handler
// may be nil if no server-to-client requests are expected.
func NewConn(rwc io.ReadWriteCloser, handler Handler) *Conn {
	c := &Conn{
		w:       rwc,
		r:       bufio.NewReader(rwc),
		c:       rwc,
		pending: make(map[string]chan *Response),
		done:    make(chan struct{}),
		handler: handler,

		inflight: make(map[string]context.CancelFunc),
	}
	c.baseCtx, c.baseStop = context.WithCancel(context.Background())
	go c.readLoop()
	return c
}

// Call sends a request and blocks until the matching response arrives, ctx is
// canceled, or the connection closes. A non-nil result is JSON-decoded from
// the response. A remote error is returned as an *Error.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	id := NewNumberID(int64(c.seq.Add(1)))
	rawParams, err := marshalParams(params)
	if err != nil {
		return err
	}
	req := &Request{JSONRPC: Version, ID: &id, Method: method, Params: rawParams}

	ch := make(chan *Response, 1)
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		return err
	}
	c.pending[id.String()] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id.String())
		c.mu.Unlock()
	}()

	if err := c.writeMessage(req); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		// Tell the peer to stop working on it; best effort, off the
		// caller's path.
		go func() {
			_ = c.Notify(context.Background(), "$/cancelRequest", map[string]any{"id": id})
		}()
		return ctx.Err()
	case <-c.done:
		return c.closeErr
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	}
}

// Notify sends a notification (a request without an ID); no response is
// awaited.
func (c *Conn) Notify(ctx context.Context, method string, params any) error {
	rawParams, err := marshalParams(params)
	if err != nil {
		return err
	}
	req := &Request{JSONRPC: Version, Method: method, Params: rawParams}
	return c.writeMessage(req)
}

// Close terminates the read loop and closes the underlying stream. Pending
// calls unblock with [ErrClosed].
func (c *Conn) Close() error {
	c.fail(ErrClosed)
	if c.c != nil {
		return c.c.Close()
	}
	return nil
}

// Done returns a channel closed when the connection fails or is closed.
func (c *Conn) Done() <-chan struct{} { return c.done }

func (c *Conn) writeMessage(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeFrame(c.w, data)
}

func (c *Conn) writeRaw(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeFrame(c.w, data)
}

func (c *Conn) readLoop() {
	for {
		body, err := readFrame(c.r)
		if err != nil {
			c.fail(err)
			return
		}
		// Discriminate a response (id, no method) from an inbound
		// request/notification (has method) without fully decoding twice.
		var probe struct {
			ID     *ID    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(body, &probe); err != nil {
			// Skip undecodable frames rather than tearing down; a peer that
			// can serve requests is told, with the null ID the spec mandates
			// when the request ID itself could not be read.
			if c.handler != nil {
				_ = c.writeRaw([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}`))
			}
			continue
		}
		if probe.Method == "" && probe.ID != nil {
			var resp Response
			if err := json.Unmarshal(body, &resp); err != nil {
				continue
			}
			c.deliver(&resp)
			continue
		}
		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			continue
		}
		c.handleInbound(&req)
	}
}

func (c *Conn) deliver(resp *Response) {
	if resp.ID == nil {
		return
	}
	c.mu.Lock()
	ch := c.pending[resp.ID.String()]
	c.mu.Unlock()
	if ch != nil {
		ch <- resp // buffered (cap 1); never blocks
	}
}

// handleInbound dispatches one inbound message. Notifications run
// synchronously on the read loop, so they are observed in wire order; each
// request runs on its own goroutine under a context that $/cancelRequest (or
// connection close) cancels. A request is therefore NOT ordered against the
// notifications that follow it.
func (c *Conn) handleInbound(req *Request) {
	if req.ID == nil { // notification
		if req.Method == "$/cancelRequest" {
			c.cancelInbound(req.Params)
			return
		}
		if c.handler != nil {
			_, _ = c.handler.Handle(c.baseCtx, req)
		}
		return
	}

	ctx, cancel := context.WithCancel(c.baseCtx)
	key := req.ID.String()
	c.mu.Lock()
	c.inflight[key] = cancel
	c.mu.Unlock()

	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.inflight, key)
			c.mu.Unlock()
			cancel()
		}()
		c.serveRequest(ctx, req)
	}()
}

func (c *Conn) cancelInbound(params json.RawMessage) {
	var p struct {
		ID *ID `json:"id"`
	}
	if json.Unmarshal(params, &p) != nil || p.ID == nil {
		return
	}
	c.mu.Lock()
	cancel := c.inflight[p.ID.String()]
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *Conn) serveRequest(ctx context.Context, req *Request) {
	var (
		result any
		err    error
	)
	if c.handler != nil {
		result, err = c.handler.Handle(ctx, req)
	} else {
		err = &Error{Code: CodeMethodNotFound, Message: "method not found: " + req.Method}
	}

	resp := &Response{JSONRPC: Version, ID: req.ID}
	switch {
	case ctx.Err() != nil && c.baseCtx.Err() == nil:
		// Canceled by the peer: the spec allows a result, but an error is
		// the form clients expect and discard.
		resp.Error = &Error{Code: CodeRequestCancelled, Message: "request cancelled"}
	case err != nil:
		var rpcErr *Error
		if !errors.As(err, &rpcErr) {
			rpcErr = &Error{Code: CodeInternalError, Message: err.Error()}
		}
		resp.Error = rpcErr
	default:
		raw, mErr := json.Marshal(result)
		if mErr != nil {
			resp.Error = &Error{Code: CodeInternalError, Message: mErr.Error()}
		} else {
			resp.Result = raw
		}
	}
	_ = c.writeMessage(resp)
}

func (c *Conn) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	c.closeErr = err
	c.baseStop()
	close(c.done)
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	if raw, ok := params.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(params)
}
