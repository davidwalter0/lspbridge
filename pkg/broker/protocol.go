// Package broker is the ADR-0013-style socket-activated LSP broker: a daemon
// that owns one warm language-server session per (projectRoot, language) and
// multiplexes it to BOTH mcp-ast (read) and ae (write) over a single unix
// socket, with lazy spawn, single-flight cold start, and idle-reap.
//
// # Shape (mirrors ADR-0013)
//
// ADR-0013 (mcp-fs as a socket-activating capability broker) is the design
// this package mirrors, scoped down to LSP sessions: connect → lazily activate
// the right server → keep it warm for its (stateful, long-lived) session →
// idle-reap after a quiescence window → clean shutdown-all on SIGTERM. Unlike
// ADR-0013's control plane this broker does no namespace/cgroup placement and
// no privilege drop — it is a workstation session cache, not a security ring —
// but it lands on the same primitives: path-based unix sockets only (abstract
// sockets are rejected, per ADR-0013 §Gap 4), the broker owns stale-socket
// cleanup on (re)bind, and each accepted connection speaks the same jsonrpc
// contract the rest of the fleet uses.
//
// # Wire protocol
//
// The broker reuses [github.com/davidwalter0/lspbridge/pkg/jsonrpc] over the
// unix connection (a net.Conn is an io.ReadWriteCloser). Three methods:
//
//   - "broker/query"    ([QueryRequest])  → the raw LSP result, verbatim.
//   - "broker/status"   (no params)       → [StatusResponse].
//   - "broker/shutdown" (no params)       → {"ok":true}, then the daemon stops.
//
// CQRS stays intact: reads (documentSymbol / references / definition / hover)
// and writes (rename → WorkspaceEdit → wsedit) hit the SAME warm session, but
// each consumer forwards its own LSP method — the broker never merges them into
// a third server.
package broker

import "encoding/json"

// Wire method names spoken over the broker's unix socket.
const (
	MethodQuery    = "broker/query"
	MethodStatus   = "broker/status"
	MethodShutdown = "broker/shutdown"
)

// QueryRequest asks the broker to run one LSP request against the warm session
// for a file.
//
// File is the ABSOLUTE source path; the broker resolves its project Context
// (root + language) from it via the resolver chain. Language, when non-empty,
// overrides the resolved languageId (and is the fallback root-language when no
// resolver claims the file). Method and Params are the LSP method name and its
// params, forwarded verbatim; Params MUST reference File's file:// URI (the
// broker syncs that document but does not rewrite the params). Text, when
// non-nil, is the file's current — possibly unsaved — content the broker
// didOpen/didChange into the session before forwarding; a nil Text reads the
// file from disk.
type QueryRequest struct {
	File     string          `json:"file"`
	Language string          `json:"language,omitempty"`
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params"`
	Text     *string         `json:"text,omitempty"`
}

// StatusResponse is the broker/status reply: one entry per warm session.
type StatusResponse struct {
	Sessions []SessionStatus `json:"sessions"`
}

// SessionStatus describes one warm session for broker/status.
type SessionStatus struct {
	// Key is the session-cache key (see projectcontext.Context.Key).
	Key string `json:"key"`
	// Language is the session's LSP languageId.
	Language string `json:"language"`
	// Root is the absolute project root the server is scoped to.
	Root string `json:"root"`
	// OpenDocs are the document URIs currently synced into the session.
	OpenDocs []string `json:"openDocs"`
	// IdleSeconds is how long since the session last served a query.
	IdleSeconds float64 `json:"idleSeconds"`
	// Inflight is the number of queries currently executing on the session.
	Inflight int `json:"inflight"`
}

// ShutdownResponse is the broker/shutdown reply.
type ShutdownResponse struct {
	OK bool `json:"ok"`
}
