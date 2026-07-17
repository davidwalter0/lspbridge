// Package jsonrpc implements the minimal subset of JSON-RPC 2.0 over a
// Content-Length-framed byte stream that the Language Server Protocol uses.
//
// It is transport-agnostic: a [Conn] runs over any [io.ReadWriteCloser],
// which in production is the stdin/stdout pipe pair of a language-server
// subprocess and in tests is an in-memory pipe.
package jsonrpc

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Version is the JSON-RPC protocol version string LSP requires.
const Version = "2.0"

// Standard JSON-RPC error codes (subset used here).
const (
	CodeMethodNotFound = -32601
	CodeInternalError  = -32603
)

// ID is a JSON-RPC request identifier. Per the specification it may be
// either a string or a number; a server echoes back whatever form the
// client sent, so ID preserves which was used.
type ID struct {
	num   int64
	str   string
	isStr bool
}

// NewNumberID returns a numeric request ID.
func NewNumberID(n int64) ID { return ID{num: n} }

// NewStringID returns a string request ID.
func NewStringID(s string) ID { return ID{str: s, isStr: true} }

// MarshalJSON renders the ID as a bare JSON number or string.
func (id ID) MarshalJSON() ([]byte, error) {
	if id.isStr {
		return json.Marshal(id.str)
	}
	return json.Marshal(id.num)
}

// UnmarshalJSON accepts either the number or the string form.
func (id *ID) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		id.isStr = true
		return json.Unmarshal(data, &id.str)
	}
	id.isStr = false
	return json.Unmarshal(data, &id.num)
}

// String returns a stable textual form of the ID, suitable as a map key.
// The type prefix keeps the numeric ID 1 distinct from the string ID "1".
func (id ID) String() string {
	if id.isStr {
		return "s:" + id.str
	}
	return "n:" + strconv.FormatInt(id.num, 10)
}

// Request is an outgoing or incoming JSON-RPC request. A nil ID marks a
// notification, for which no response is expected or produced.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *ID             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC response. Exactly one of Result or Error is set.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *ID             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a JSON-RPC error object. It implements the error interface so a
// remote error can be returned directly from [Conn.Call].
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("jsonrpc: %d %s", e.Code, e.Message)
}
