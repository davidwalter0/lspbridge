package orglsp

import (
	"sort"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// FoldingRanges computes textDocument/foldingRange results for text: one
// range per heading subtree, one per closed #+begin_/#+end_ block, and one
// per :NAME:/:END: drawer.
//
// Unlike [DocumentSymbols], this does not consult orgast at all — every
// input is a direct line scan ([scanHeadingLines], [scanBlocks],
// [scanDrawers]), because (a) a fold range needs no semantic content
// (title, tags, language) — only a line span — and (b) orgast has no typed
// Drawer projection to consult even if it wanted to (see its package doc's
// scope-discipline note).
//
// Headings are folded even inside a :crypt: subtree. That is deliberate,
// not an oversight: folding away encrypted content is exactly the
// desirable default (Emacs itself folds :crypt: headings routinely), and a
// line-span reveals nothing about body content — see the crypt-aware
// traversal note in [github.com/davidwalter0/org/orgast]'s package doc,
// which draws the same line (the heading node, and hence its physical
// extent, is always visible; only its body text is opaque).
func FoldingRanges(text string) []lsp.FoldingRange {
	lines := splitLines(text)
	headingLines := scanHeadingLines(lines)

	var out []lsp.FoldingRange
	for _, hl := range headingLines {
		end := endOfSubtree(headingLines, hl.Line, hl.Level, len(lines))
		if end-1 <= hl.Line {
			continue // heading immediately followed by another: nothing to fold
		}
		out = append(out, lsp.FoldingRange{StartLine: hl.Line, EndLine: end - 1, Kind: lsp.FoldingRangeKindRegion})
	}

	closedBlocks, _ := scanBlocks(lines)
	for _, b := range closedBlocks {
		if b.EndLine <= b.BeginLine {
			continue
		}
		// No single standardized FoldingRangeKind fits "code/example block";
		// leave Kind unset rather than overload "region" or invent a
		// nonstandard one — the client still folds it, just uncategorized.
		out = append(out, lsp.FoldingRange{StartLine: b.BeginLine, EndLine: b.EndLine})
	}

	for _, d := range scanDrawers(lines) {
		if d.EndLine <= d.BeginLine {
			continue
		}
		out = append(out, lsp.FoldingRange{StartLine: d.BeginLine, EndLine: d.EndLine})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].StartLine < out[j].StartLine })
	return out
}
