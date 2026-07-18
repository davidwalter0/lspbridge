package lsp

import "encoding/json"

// TextDocumentEdit is one entry of a WorkspaceEdit.documentChanges array: a set
// of TextEdits against a single (optionally versioned) document. lspbridge needs
// only the URI and edits; the document version and any per-edit annotationIds
// are ignored.
type TextDocumentEdit struct {
	TextDocument OptionalVersionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []TextEdit                              `json:"edits"`
}

// OptionalVersionedTextDocumentIdentifier identifies a document with an optional
// version. Servers send version: null when the version is unknown (pyright's
// rename does), so Version is a pointer.
type OptionalVersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version *int        `json:"version"`
}

// UnmarshalJSON normalizes BOTH WorkspaceEdit wire forms into Changes:
//
//   - "changes": a map of URI → TextEdit[] (the legacy form a client gets when
//     it does not advertise documentChanges support); and
//   - "documentChanges": a TextDocumentEdit[] — what pyright and most modern
//     servers return, EVEN when the client does not advertise documentChanges
//     (verified against pyright 1.1.411: rename replies with documentChanges
//     regardless of the advertised capability).
//
// TextEdits from documentChanges are folded into Changes keyed by document URI,
// so the write path ([github.com/davidwalter0/lspbridge/pkg/wsedit.FromWorkspaceEdit])
// reads one uniform map and never has to know which form the server used.
// Resource operations (create/rename/delete file) that may appear in
// documentChanges carry no textDocument.edits and are skipped — a symbol rename
// never emits them; a future write feature that needs them would model them
// explicitly rather than silently dropping them here.
func (we *WorkspaceEdit) UnmarshalJSON(data []byte) error {
	var shim struct {
		Changes         map[DocumentURI][]TextEdit `json:"changes"`
		DocumentChanges []json.RawMessage          `json:"documentChanges"`
	}
	if err := json.Unmarshal(data, &shim); err != nil {
		return err
	}
	we.Changes = shim.Changes
	for _, raw := range shim.DocumentChanges {
		var tde TextDocumentEdit
		if err := json.Unmarshal(raw, &tde); err != nil {
			return err
		}
		if tde.TextDocument.URI == "" || len(tde.Edits) == 0 {
			continue // resource operation or empty entry
		}
		if we.Changes == nil {
			we.Changes = make(map[DocumentURI][]TextEdit)
		}
		we.Changes[tde.TextDocument.URI] = append(we.Changes[tde.TextDocument.URI], tde.Edits...)
	}
	return nil
}
