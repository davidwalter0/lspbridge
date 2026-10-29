package lsp

import (
	"encoding/json"
	"testing"
)

func TestChangeEventWholeDocumentWireShape(t *testing.T) {
	b, err := json.Marshal(TextDocumentContentChangeEvent{Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"text":"x"}` {
		t.Fatalf("whole-document change must stay {\"text\":...}, got %s", b)
	}
}

func TestChangeEventIncrementalRoundTrip(t *testing.T) {
	in := `{"range":{"start":{"line":1,"character":2},"end":{"line":1,"character":4}},"text":"ab"}`
	var ev TextDocumentContentChangeEvent
	if err := json.Unmarshal([]byte(in), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Range == nil || ev.Range.Start.Character != 2 || ev.Range.End.Character != 4 || ev.Text != "ab" {
		t.Fatalf("decoded wrong: %+v", ev)
	}
}
