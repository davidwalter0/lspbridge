package orglsp

import (
	"os"
	"strings"
	"testing"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

const fixturePath = "testdata/session-fixture.org"

func readFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(data)
}

// TestDocumentSymbolsGoldenTree is the golden nested documentSymbol test the
// task calls for: it walks the full tree produced over a session-trace-
// shaped fixture (properties drawer, nested headings, a src block, and a
// :crypt: subtree) and asserts both its shape and that no crypt content
// ever surfaces.
func TestDocumentSymbolsGoldenTree(t *testing.T) {
	text := readFixture(t)
	lines := splitLines(text)
	syms := DocumentSymbols(fixturePath, text)

	if len(syms) != 3 {
		t.Fatalf("want 3 root symbols (Session State, Design Notes, Footnotes), got %d: %s", len(syms), dumpTree(syms, 0))
	}

	sessionState, designNotes, footnotes := syms[0], syms[1], syms[2]

	// --- Session State ---
	if sessionState.Name != "Session State" {
		t.Fatalf("root[0].Name = %q, want %q", sessionState.Name, "Session State")
	}
	if sessionState.Kind != lsp.SymbolKindNamespace {
		t.Errorf("Session State.Kind = %v, want Namespace (has children)", sessionState.Kind)
	}
	wantStart := mustLineIndex(t, lines, "* Session State")
	wantEnd := mustLineIndex(t, lines, "* Design Notes")
	if sessionState.Range.Start.Line != wantStart {
		t.Errorf("Session State.Range.Start.Line = %d, want %d", sessionState.Range.Start.Line, wantStart)
	}
	if sessionState.Range.End.Line != wantEnd {
		t.Errorf("Session State.Range.End.Line = %d, want %d (start of next top-level heading)", sessionState.Range.End.Line, wantEnd)
	}
	if len(sessionState.Children) != 2 {
		t.Fatalf("Session State children = %d, want 2: %s", len(sessionState.Children), dumpTree(sessionState.Children, 0))
	}
	status, completed := sessionState.Children[0], sessionState.Children[1]
	if status.Name != "Status" || status.Kind != lsp.SymbolKindString || len(status.Children) != 0 {
		t.Errorf("Status = %+v, want leaf String named Status", status)
	}
	if completed.Name != "Completed" || completed.Kind != lsp.SymbolKindString || len(completed.Children) != 0 {
		t.Errorf("Completed = %+v, want leaf String named Completed", completed)
	}

	// --- Design Notes ---
	if designNotes.Name != "Design Notes" {
		t.Fatalf("root[1].Name = %q, want %q", designNotes.Name, "Design Notes")
	}
	if designNotes.Kind != lsp.SymbolKindNamespace {
		t.Errorf("Design Notes.Kind = %v, want Namespace", designNotes.Kind)
	}
	if len(designNotes.Children) != 2 {
		t.Fatalf("Design Notes children = %d, want 2 (Implementation, Secrets): %s", len(designNotes.Children), dumpTree(designNotes.Children, 0))
	}
	implementation, secrets := designNotes.Children[0], designNotes.Children[1]

	if implementation.Name != "Implementation" {
		t.Fatalf("Design Notes.Children[0].Name = %q, want %q", implementation.Name, "Implementation")
	}
	// Implementation has no SUB-HEADING, but it does have a src-block child,
	// so it must still render as Namespace (see symbolKindForHeading's doc).
	if implementation.Kind != lsp.SymbolKindNamespace {
		t.Errorf("Implementation.Kind = %v, want Namespace (has a src-block child)", implementation.Kind)
	}
	if len(implementation.Children) != 1 {
		t.Fatalf("Implementation children = %d, want 1 (the src block): %s", len(implementation.Children), dumpTree(implementation.Children, 0))
	}
	srcSym := implementation.Children[0]
	if srcSym.Name != "src:go" || srcSym.Kind != lsp.SymbolKindConstant {
		t.Errorf("src child = %+v, want Name=src:go Kind=Constant", srcSym)
	}

	if secrets.Name != "Secrets" {
		t.Fatalf("Design Notes.Children[1].Name = %q, want %q", secrets.Name, "Secrets")
	}
	if !strings.Contains(secrets.Detail, "crypt") {
		t.Errorf("Secrets.Detail = %q, want it to mention the crypt tag", secrets.Detail)
	}
	// The crypt subtree's nested heading ("Nested under secrets") must NEVER
	// appear as a child — orgast never descends into it — so Secrets is a
	// leaf and renders as String, same as any other childless heading.
	if len(secrets.Children) != 0 {
		t.Errorf("Secrets.Children = %+v, want none (crypt subtree must not be descended)", secrets.Children)
	}
	if secrets.Kind != lsp.SymbolKindString {
		t.Errorf("Secrets.Kind = %v, want String (leaf, crypt subtree not descended)", secrets.Kind)
	}

	// --- Footnotes ---
	if footnotes.Name != "Footnotes" || footnotes.Kind != lsp.SymbolKindString || len(footnotes.Children) != 0 {
		t.Errorf("Footnotes = %+v, want leaf String named Footnotes", footnotes)
	}

	// --- Whole-tree crypt non-leakage check ---
	// No Name or Detail anywhere in the tree may mention the secret body
	// text, and nothing named after the nested-under-crypt heading may
	// appear at all.
	forbidden := []string{"must never surface", "Nested under secrets"}
	walkSymbols(syms, func(s lsp.DocumentSymbol) {
		for _, f := range forbidden {
			if strings.Contains(s.Name, f) || strings.Contains(s.Detail, f) {
				t.Errorf("symbol %+v leaks forbidden crypt text %q", s, f)
			}
		}
	})
}

func TestDocumentSymbolsEmptyDocument(t *testing.T) {
	if got := DocumentSymbols("empty.org", ""); got != nil {
		t.Errorf("DocumentSymbols(empty) = %+v, want nil", got)
	}
}

func TestDocumentSymbolsNoHeadingsJustSrcBlock(t *testing.T) {
	text := "#+begin_src go\nfmt.Println(1)\n#+end_src\n"
	syms := DocumentSymbols("nohead.org", text)
	if len(syms) != 1 || syms[0].Name != "src:go" {
		t.Fatalf("syms = %+v, want a single root src:go symbol", syms)
	}
}

func TestHeadingNameEmptyTitle(t *testing.T) {
	text := "* \nbody\n"
	syms := DocumentSymbols("blank.org", text)
	if len(syms) != 1 || syms[0].Name != "(untitled)" {
		t.Fatalf("syms = %+v, want a single (untitled) root symbol", syms)
	}
}

// dumpTree renders a symbol tree compactly for failure messages.
func dumpTree(syms []lsp.DocumentSymbol, depth int) string {
	var b strings.Builder
	for _, s := range syms {
		b.WriteString(strings.Repeat("  ", depth))
		b.WriteString(s.Name)
		b.WriteString("\n")
		b.WriteString(dumpTree(s.Children, depth+1))
	}
	return b.String()
}

// walkSymbols visits every symbol in the tree, depth-first.
func walkSymbols(syms []lsp.DocumentSymbol, fn func(lsp.DocumentSymbol)) {
	for _, s := range syms {
		fn(s)
		walkSymbols(s.Children, fn)
	}
}
