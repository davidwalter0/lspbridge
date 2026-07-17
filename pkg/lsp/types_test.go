package lsp

import (
	"encoding/json"
	"testing"
)

func TestWorkspaceEditRoundTrip(t *testing.T) {
	we := WorkspaceEdit{
		Changes: map[DocumentURI][]TextEdit{
			"file:///a.py": {
				{Range: Range{Start: Position{Line: 1, Character: 0}, End: Position{Line: 1, Character: 3}}, NewText: "foo"},
			},
		},
	}
	data, err := json.Marshal(we)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back WorkspaceEdit
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	edits := back.Changes["file:///a.py"]
	if len(edits) != 1 || edits[0].NewText != "foo" || edits[0].Range.End.Character != 3 {
		t.Errorf("round trip mismatch: %+v", back)
	}
}

func TestDiagnosticCodeAcceptsIntAndString(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"int code", `{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"code":42,"message":"x"}`},
		{"string code", `{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"code":"E501","message":"x"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Diagnostic
			if err := json.Unmarshal([]byte(tt.raw), &d); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if d.Message != "x" {
				t.Errorf("message = %q", d.Message)
			}
		})
	}
}
