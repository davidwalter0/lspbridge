// Package lsp provides a minimal Language Server Protocol client: the
// protocol types the bridge maps to the shared structast DTO, and a
// lifecycle client (initialize / didOpen / didChange / shutdown) built on
// [github.com/davidwalter0/lspbridge/pkg/jsonrpc].
//
// The type set is intentionally the subset the read (references, symbols,
// diagnostics) and write (WorkspaceEdit) paths need; it is extended per
// server as capabilities are wired in.
package lsp

// DocumentURI is an LSP document URI, e.g. "file:///abs/path.py".
type DocumentURI string

// Position is a zero-based line/character offset. Per the LSP spec character
// offsets are UTF-16 code units by default; callers that map to the structast
// DTO (1-indexed, UTF-8) convert at the boundary.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a [Start, End) span within a document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a range within a specific document.
type Location struct {
	URI   DocumentURI `json:"uri"`
	Range Range       `json:"range"`
}

// TextEdit replaces Range with NewText.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// WorkspaceEdit is a set of per-document text edits. The bridge maps this to
// an ae transaction.Plan on the write path; the LSP server computes it purely
// and never writes to disk.
//
// On the wire an edit arrives as EITHER "changes" (a URI→edits map) OR
// "documentChanges" (a TextDocumentEdit array); [WorkspaceEdit.UnmarshalJSON]
// normalizes both into Changes, so consumers only ever read this one field.
type WorkspaceEdit struct {
	Changes map[DocumentURI][]TextEdit `json:"changes,omitempty"`
}

// DiagnosticSeverity is the LSP severity enumeration.
type DiagnosticSeverity int

// LSP diagnostic severities.
const (
	SeverityError       DiagnosticSeverity = 1
	SeverityWarning     DiagnosticSeverity = 2
	SeverityInformation DiagnosticSeverity = 3
	SeverityHint        DiagnosticSeverity = 4
)

// Diagnostic is a problem reported at a range. Code is any because the LSP
// spec allows either an integer or a string code.
type Diagnostic struct {
	Range    Range              `json:"range"`
	Severity DiagnosticSeverity `json:"severity,omitempty"`
	Code     any                `json:"code,omitempty"`
	Source   string             `json:"source,omitempty"`
	Message  string             `json:"message"`
}
