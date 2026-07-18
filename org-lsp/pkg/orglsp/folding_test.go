package orglsp

import (
	"sort"
	"strings"
	"testing"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

func TestFoldingRangesGoldenFixture(t *testing.T) {
	text := readFixture(t)
	lines := splitLines(text)
	ranges := FoldingRanges(text)

	byStart := make(map[int]lsp.FoldingRange, len(ranges))
	for _, r := range ranges {
		byStart[r.StartLine] = r
	}

	// One fold per top-level heading, per sub-heading, per drawer, per block.
	topProps := mustLineIndex(t, lines, ":PROPERTIES:") // file-level drawer
	sessionState := mustLineIndex(t, lines, "* Session State")
	sessionProps := mustLineIndexFrom(t, lines, sessionState, ":PROPERTIES:")
	designNotes := mustLineIndex(t, lines, "* Design Notes")
	srcBegin := mustLineIndex(t, lines, "#+begin_src")
	srcEnd := mustLineIndex(t, lines, "#+end_src")
	footnotes := mustLineIndex(t, lines, "* Footnotes")

	for _, tc := range []struct {
		name  string
		start int
	}{
		{"file-level PROPERTIES drawer", topProps},
		{"Session State heading", sessionState},
		{"Session State's own PROPERTIES drawer", sessionProps},
		{"Design Notes heading", designNotes},
		{"src block", srcBegin},
	} {
		if _, ok := byStart[tc.start]; !ok {
			t.Errorf("%s (line %d): no folding range found; got starts %v", tc.name, tc.start, sortedFoldStarts(byStart))
		}
	}

	if r := byStart[srcBegin]; r.EndLine != srcEnd {
		t.Errorf("src block fold = %+v, want EndLine %d", r, srcEnd)
	}
	if r, ok := byStart[designNotes]; ok && r.EndLine != footnotes-1 {
		t.Errorf("Design Notes fold = %+v, want EndLine %d (line before Footnotes)", r, footnotes-1)
	}

	// Folding does NOT skip the crypt subtree — it still gets an entry (see
	// FoldingRanges' doc: a line span reveals no content).
	secrets := mustLineIndex(t, lines, ":crypt:")
	if _, ok := byStart[secrets]; !ok {
		t.Errorf("Secrets heading (line %d, :crypt:): no folding range found (crypt headings should still fold)", secrets)
	}
}

func TestFoldingRangesNoNestedHeadingsNoFold(t *testing.T) {
	// Two adjacent same-level headings: the first has nothing to fold.
	text := "* One\n* Two\nbody\n"
	ranges := FoldingRanges(text)
	for _, r := range ranges {
		if r.StartLine == 0 {
			t.Errorf("heading with no body got a folding range: %+v", r)
		}
	}
}

func TestFoldingRangesEmptyDocument(t *testing.T) {
	if got := FoldingRanges(""); len(got) != 0 {
		t.Errorf("FoldingRanges(empty) = %+v, want none", got)
	}
}

func mustLineIndexFrom(t *testing.T, lines []string, from int, substr string) int {
	t.Helper()
	for i := from; i < len(lines); i++ {
		if strings.Contains(lines[i], substr) {
			return i
		}
	}
	t.Fatalf("no line containing %q at or after line %d", substr, from)
	return -1
}

func sortedFoldStarts(m map[int]lsp.FoldingRange) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
