// Package lsp provides a minimal Language Server Protocol client: the
// protocol types the bridge maps to the shared structast DTO, and a
// lifecycle client (initialize / didOpen / didChange / shutdown) built on
// [github.com/davidwalter0/lspbridge/pkg/jsonrpc].
//
// The type set is intentionally the subset the read (references, symbols,
// diagnostics) and write (WorkspaceEdit) paths need; it is extended per
// server as capabilities are wired in. Most of that type set is generated
// from the pinned LSP metaModel.json — see types_gen.go (the DO-NOT-EDIT
// output) and cmd/lspgen/config.go (the allowlist, and the documented
// rationale for the small set of types below and elsewhere in this package
// that stay hand-written because they carry custom marshal logic or a
// deliberate spec subset).
package lsp

// DocumentURI is an LSP document URI, e.g. "file:///abs/path.py".
type DocumentURI string

// WorkspaceEdit is a set of per-document text edits. The bridge maps this to
// an ae transaction.Plan on the write path; the LSP server computes it purely
// and never writes to disk.
//
// On the wire an edit arrives as EITHER "changes" (a URI→edits map) OR
// "documentChanges" (a TextDocumentEdit array); [WorkspaceEdit.UnmarshalJSON]
// normalizes both into Changes, so consumers only ever read this one field.
//
// WorkspaceEdit is hand-written rather than generated (cmd/lspgen/config.go):
// the wire-form normalization above is behavior, not a mechanical data shape.
type WorkspaceEdit struct {
	Changes map[DocumentURI][]TextEdit `json:"changes,omitempty"`
}
