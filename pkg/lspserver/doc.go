// Package lspserver is a zero-dependency Language Server Protocol server
// skeleton: the initialize / initialized / shutdown / exit lifecycle, a
// versioned document store with full and incremental sync, and the
// byte offset <-> (line, UTF-16 character) conversions in both directions.
//
// A concrete server supplies [Options]: capabilities, a [jsonrpc.Handler] for
// its feature requests, and optional document hooks. Requests arrive on their
// own goroutines (see pkg/jsonrpc) while notifications, including the text
// synchronisation ones, are applied in wire order on the read loop; document
// snapshots handed to handlers are immutable, so a feature request never
// observes a half-applied change.
//
// Positions always count UTF-16 code units: the server advertises no other
// positionEncoding, and the spec makes UTF-16 the mandatory default.
package lspserver
