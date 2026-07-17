package lsp

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
)

// publishDiagnosticsMethod is the server-to-client notification method LSP
// servers use to report per-document diagnostics.
const publishDiagnosticsMethod = "textDocument/publishDiagnostics"

// PublishDiagnosticsParams is the payload of the server-to-client
// "textDocument/publishDiagnostics" notification.
type PublishDiagnosticsParams struct {
	URI         DocumentURI  `json:"uri"`
	Version     int          `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// pollInterval is how often [DiagnosticsCollector.Wait] rechecks for a
// non-empty diagnostics batch. It trades a small amount of latency for a
// dependency-free, leak-free implementation (no extra goroutine per Wait).
const pollInterval = 50 * time.Millisecond

// DiagnosticsCollector is a [jsonrpc.Handler] that collects
// textDocument/publishDiagnostics notifications per document URI. It is a
// drop-in handler for [github.com/davidwalter0/lspbridge/pkg/server.Launch]:
// every other inbound method (other notifications, and any server-to-client
// requests) is acknowledged as a no-op success.
//
// The zero value is not usable; construct with [NewDiagnosticsCollector].
type DiagnosticsCollector struct {
	mu     sync.Mutex
	latest map[DocumentURI][]Diagnostic
	seen   map[DocumentURI]bool
}

// NewDiagnosticsCollector returns an empty collector.
func NewDiagnosticsCollector() *DiagnosticsCollector {
	return &DiagnosticsCollector{
		latest: make(map[DocumentURI][]Diagnostic),
		seen:   make(map[DocumentURI]bool),
	}
}

// Handle implements [jsonrpc.Handler]. It records
// textDocument/publishDiagnostics notifications; any other method (a
// notification or a server-to-client request) is a no-op that returns
// (nil, nil), so the collector can be the sole handler for a language-server
// connection.
func (d *DiagnosticsCollector) Handle(_ context.Context, req *jsonrpc.Request) (any, error) {
	if req.Method != publishDiagnosticsMethod {
		return nil, nil
	}
	var params PublishDiagnosticsParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		// A malformed notification is dropped rather than torn down; the
		// connection's read loop already treats undecodable frames the
		// same way.
		return nil, nil
	}
	d.mu.Lock()
	d.latest[params.URI] = params.Diagnostics
	d.seen[params.URI] = true
	d.mu.Unlock()
	return nil, nil
}

// Diagnostics returns the diagnostics from the most recent
// publishDiagnostics notification for uri, and whether any notification for
// uri has arrived yet.
func (d *DiagnosticsCollector) Diagnostics(uri DocumentURI) ([]Diagnostic, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.latest[uri], d.seen[uri]
}

// Wait blocks until at least one diagnostic has been published for uri, or
// ctx is done first. Servers commonly publish an interim empty batch before
// the final one lands, so Wait deliberately waits for a non-empty batch
// rather than returning on the first notification. If diagnostics already
// arrived before Wait was called, it returns immediately.
func (d *DiagnosticsCollector) Wait(ctx context.Context, uri DocumentURI) ([]Diagnostic, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		d.mu.Lock()
		diags := d.latest[uri]
		d.mu.Unlock()
		if len(diags) > 0 {
			return diags, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
