package orglsp

import (
	"strings"
	"testing"

	"github.com/davidwalter0/org/orgast"
)

const cryptSample = `#+TITLE: Sample

* Top
Intro.

** Safe subsection
Some prose.

#+begin_src go
func f() {}
#+end_src

** Secrets                                                            :crypt:
Secret body that must not be read.

*** Nested under secrets
Also must not appear.

** After secrets
Trailing prose.
`

func TestScanHeadingLines(t *testing.T) {
	lines := splitLines(cryptSample)
	hs := scanHeadingLines(lines)
	var titles []string
	for _, h := range hs {
		titles = append(titles, h.Text)
	}
	// The raw scan sees EVERY heading line, including ones nested under
	// :crypt: — unlike orgast.Headings, which excludes them.
	want := []string{"Top", "Safe subsection", "Secrets                                                            :crypt:", "Nested under secrets", "After secrets"}
	if len(titles) != len(want) {
		t.Fatalf("scanHeadingLines = %q, want %d entries", titles, len(want))
	}
	for i, w := range want {
		if titles[i] != w {
			t.Errorf("titles[%d] = %q, want %q", i, titles[i], w)
		}
	}
}

func TestMatchHeadingStartsAlignsWithOrgastCryptFiltering(t *testing.T) {
	lines := splitLines(cryptSample)
	doc := orgast.ParseString("sample.org", cryptSample)
	headings := orgast.Headings(doc)

	// orgast excludes "Nested under secrets" (a child of a :crypt: heading);
	// the raw line scan found 5 heading lines, orgast reports 4.
	if len(headings) != 4 {
		t.Fatalf("orgast.Headings = %d entries, want 4 (sanity check on the fixture)", len(headings))
	}

	headingLines := scanHeadingLines(lines)
	starts := matchHeadingStarts(headings, headingLines)
	if len(starts) != len(headings) {
		t.Fatalf("matchHeadingStarts returned %d starts for %d headings", len(starts), len(headings))
	}

	for i, h := range headings {
		line := lines[starts[i]]
		if !strings.Contains(line, h.Title) {
			t.Errorf("heading %d (%q) matched to line %d (%q): title not contained", i, h.Title, starts[i], line)
		}
	}
	// And the matched lines must be strictly increasing (each heading's
	// start advances past the previous one).
	for i := 1; i < len(starts); i++ {
		if starts[i] <= starts[i-1] {
			t.Errorf("starts not strictly increasing: starts[%d]=%d <= starts[%d]=%d", i, starts[i], i-1, starts[i-1])
		}
	}
}

func TestEndOfSubtree(t *testing.T) {
	lines := splitLines(cryptSample)
	headingLines := scanHeadingLines(lines)

	topLine := mustLineIndex(t, lines, "* Top")
	end := endOfSubtree(headingLines, topLine, 1, len(lines))
	if end != len(lines) {
		t.Errorf("end of the last top-level heading's subtree = %d, want EOF (%d)", end, len(lines))
	}

	safeLine := mustLineIndex(t, lines, "** Safe subsection")
	secretsLine := mustLineIndex(t, lines, ":crypt:")
	end = endOfSubtree(headingLines, safeLine, 2, len(lines))
	if end != secretsLine {
		t.Errorf("end of 'Safe subsection' subtree = %d, want %d (start of the next level<=2 heading)", end, secretsLine)
	}
}

func TestEnclosingHeadingLevel(t *testing.T) {
	lines := splitLines(cryptSample)
	headingLines := scanHeadingLines(lines)
	srcLine := mustLineIndex(t, lines, "#+begin_src")
	if got := enclosingHeadingLevel(headingLines, srcLine); got != 2 {
		t.Errorf("enclosingHeadingLevel(before src block) = %d, want 2 (Safe subsection)", got)
	}
	if got := enclosingHeadingLevel(headingLines, 0); got != 0 {
		t.Errorf("enclosingHeadingLevel(before any heading) = %d, want 0", got)
	}
}

func TestScanBlocksClosedAndUnclosed(t *testing.T) {
	text := "#+begin_src go\nx\n#+end_src\n#+begin_example\nunclosed\n"
	lines := splitLines(text)
	closed, unclosed := scanBlocks(lines)
	if len(closed) != 1 || closed[0].Name != "SRC" || closed[0].BeginLine != 0 || closed[0].EndLine != 2 {
		t.Errorf("closed = %+v", closed)
	}
	if len(unclosed) != 1 || unclosed[0].Name != "EXAMPLE" || unclosed[0].Line != 3 {
		t.Errorf("unclosed = %+v", unclosed)
	}
}

func TestScanBlocksParamsCaptured(t *testing.T) {
	text := "#+begin_src go :results silent\nbody\n#+end_src\n"
	closed, _ := scanBlocks(splitLines(text))
	if len(closed) != 1 {
		t.Fatalf("closed = %+v", closed)
	}
	if !strings.HasPrefix(closed[0].Params, "go") {
		t.Errorf("Params = %q, want to start with lang %q", closed[0].Params, "go")
	}
}

func TestMatchSrcBlockLines(t *testing.T) {
	doc := orgast.ParseString("sample.org", cryptSample)
	blocks := orgast.SrcBlocks(doc)
	if len(blocks) != 1 {
		t.Fatalf("orgast.SrcBlocks = %d, want 1", len(blocks))
	}
	closed, _ := scanBlocks(splitLines(cryptSample))
	matched := matchSrcBlockLines(blocks, closed)
	if len(matched) != 1 || matched[0].Name != "SRC" {
		t.Fatalf("matchSrcBlockLines = %+v", matched)
	}
}

func TestScanDrawers(t *testing.T) {
	text := ":PROPERTIES:\n:ID: x\n:END:\nbody\n:LOGBOOK:\nnote\n:END:\n"
	spans := scanDrawers(splitLines(text))
	if len(spans) != 2 {
		t.Fatalf("scanDrawers = %+v, want 2 spans", spans)
	}
	if spans[0].Name != "PROPERTIES" || spans[0].BeginLine != 0 || spans[0].EndLine != 2 {
		t.Errorf("spans[0] = %+v", spans[0])
	}
	if spans[1].Name != "LOGBOOK" || spans[1].BeginLine != 4 || spans[1].EndLine != 6 {
		t.Errorf("spans[1] = %+v", spans[1])
	}
}

func TestScanDrawersUnclosedIsOmitted(t *testing.T) {
	text := ":PROPERTIES:\n:ID: x\n"
	spans := scanDrawers(splitLines(text))
	if len(spans) != 0 {
		t.Errorf("scanDrawers(unclosed) = %+v, want none", spans)
	}
}

func TestFindLineContaining(t *testing.T) {
	lines := []string{"alpha", "beta needle", "gamma needle"}
	if got := findLineContaining(lines, 0, "needle"); got != 1 {
		t.Errorf("findLineContaining from 0 = %d, want 1", got)
	}
	if got := findLineContaining(lines, 2, "needle"); got != 2 {
		t.Errorf("findLineContaining from 2 = %d, want 2", got)
	}
	if got := findLineContaining(lines, 0, "missing"); got != -1 {
		t.Errorf("findLineContaining(missing) = %d, want -1", got)
	}
	if got := findLineContaining(lines, 0, ""); got != -1 {
		t.Errorf("findLineContaining(empty needle) = %d, want -1", got)
	}
}

func TestUTF16Len(t *testing.T) {
	cases := map[string]int{
		"":       0,
		"abc":    3,
		"héllo":  5,
		"日本語": 3,
		"𝔘":       2, // U+1D518, outside the BMP: a UTF-16 surrogate pair
	}
	for s, want := range cases {
		if got := utf16Len(s); got != want {
			t.Errorf("utf16Len(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestSplitLines(t *testing.T) {
	if got := splitLines("a\nb\nc"); len(got) != 3 {
		t.Errorf("splitLines(no trailing nl) = %v", got)
	}
	if got := splitLines("a\nb\n"); len(got) != 3 || got[2] != "" {
		t.Errorf("splitLines(trailing nl) = %v, want a trailing empty element", got)
	}
}

// mustLineIndex returns the index of the first line containing substr, or
// fails the test — a robustness aid so fixture-line assertions never rely on
// hand-counted line numbers.
func mustLineIndex(t *testing.T, lines []string, substr string) int {
	t.Helper()
	for i, l := range lines {
		if strings.Contains(l, substr) {
			return i
		}
	}
	t.Fatalf("no line containing %q in fixture", substr)
	return -1
}
