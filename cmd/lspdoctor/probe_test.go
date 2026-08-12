package main

import (
	"encoding/json"
	"testing"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

func TestFindSelectionRangeHierarchical(t *testing.T) {
	hier := []lsp.DocumentSymbol{
		{
			Name:           "greet",
			Kind:           12,
			Range:          lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 1, Character: 0}},
			SelectionRange: lsp.Range{Start: lsp.Position{Line: 0, Character: 4}, End: lsp.Position{Line: 0, Character: 9}},
		},
		{
			Name:           "main",
			Kind:           12,
			Range:          lsp.Range{Start: lsp.Position{Line: 3, Character: 0}, End: lsp.Position{Line: 5, Character: 0}},
			SelectionRange: lsp.Range{Start: lsp.Position{Line: 3, Character: 4}, End: lsp.Position{Line: 3, Character: 8}},
		},
	}
	raw, err := json.Marshal(hier)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	rng, ok := findSelectionRange(raw, "greet")
	if !ok {
		t.Fatal("expected to find greet")
	}
	if rng.Start.Line != 0 || rng.Start.Character != 4 {
		t.Fatalf("wrong selection range: %+v", rng)
	}
}

func TestFindSelectionRangeFlat(t *testing.T) {
	flat := []lsp.SymbolInformation{
		{
			Name: "greet",
			Kind: 12,
			Location: lsp.Location{
				URI:   "file:///a.py",
				Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 4}, End: lsp.Position{Line: 0, Character: 9}},
			},
		},
	}
	raw, err := json.Marshal(flat)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	rng, ok := findSelectionRange(raw, "greet")
	if !ok {
		t.Fatal("expected to find greet in the flat shape")
	}
	if rng.Start.Character != 4 {
		t.Fatalf("wrong range: %+v", rng)
	}
}

func TestFindSelectionRangeDoesNotConfuseShapes(t *testing.T) {
	// A flat SymbolInformation slice has no "selectionRange" key at all.
	// The first draft of findSelectionRange unmarshaled straight into
	// []DocumentSymbol and checked len()==0 to decide whether to try the
	// flat shape — encoding/json accepts the missing keys silently, so
	// that draft would have "found" greet here with a zero-valued Range
	// instead of falling through to the real data. Guard against
	// regressing to that.
	flat := []lsp.SymbolInformation{{
		Name:     "greet",
		Kind:     12,
		Location: lsp.Location{URI: "file:///a.py", Range: lsp.Range{Start: lsp.Position{Line: 7, Character: 2}, End: lsp.Position{Line: 7, Character: 7}}},
	}}
	raw, err := json.Marshal(flat)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	rng, ok := findSelectionRange(raw, "greet")
	if !ok {
		t.Fatal("expected to find greet")
	}
	if rng.Start.Line != 7 || rng.Start.Character != 2 {
		t.Fatalf("got the zero-valued DocumentSymbol range instead of the real flat one: %+v", rng)
	}
}

func TestFindSelectionRangeMissing(t *testing.T) {
	hier := []lsp.DocumentSymbol{{
		Name:           "other",
		SelectionRange: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 0, Character: 1}},
	}}
	raw, err := json.Marshal(hier)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if _, ok := findSelectionRange(raw, "greet"); ok {
		t.Fatal("should not find a symbol that is not present")
	}
}

func TestFindSelectionRangeNullAndEmpty(t *testing.T) {
	if _, ok := findSelectionRange([]byte(`null`), "greet"); ok {
		t.Fatal("null result should never match")
	}
	if _, ok := findSelectionRange([]byte(`[]`), "greet"); ok {
		t.Fatal("empty result should never match")
	}
}
