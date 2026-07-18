package orglsp

import (
	"sort"
	"strings"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/org/orgast"
)

// symbolNode is the intermediate, mutable tree node [DocumentSymbols] builds
// before rendering to the wire [lsp.DocumentSymbol] shape. Headings and src
// blocks are merged into one flat, line-sorted list of these and nested by a
// single stack algorithm ([buildSymbolTree]) keyed on level — a src block is
// simply assigned a level of (enclosing heading's level + 1), so it nests
// under its heading exactly like a deeper sub-heading would.
type symbolNode struct {
	level     int
	line      int
	isHeading bool // false for a src-block node; Kind is fixed, not recomputed
	sym       lsp.DocumentSymbol
	children  []*symbolNode
}

// DocumentSymbols parses text (org-mode) as path and returns the nested
// textDocument/documentSymbol tree: one [lsp.DocumentSymbol] per heading —
// nested by outline level, Ranges from the [lineindex.go] position oracle —
// plus each heading's own SRC blocks attached as lightweight leaf children
// named "src:<lang>".
//
// Crypt-aware by construction, not by special-casing here: orgast.Headings
// and orgast.SrcBlocks already exclude everything inside a :crypt: subtree's
// body (a crypt-tagged heading itself is still returned, as an opaque leaf —
// see [github.com/davidwalter0/org/orgast]'s package doc), so this function
// never sees, and can never leak, decrypted content.
func DocumentSymbols(path, text string) []lsp.DocumentSymbol {
	doc := orgast.ParseString(path, text)
	lines := splitLines(text)

	headings := orgast.Headings(doc)
	headingLines := scanHeadingLines(lines)
	hStarts := matchHeadingStarts(headings, headingLines)

	srcBlocks := orgast.SrcBlocks(doc)
	closedBlocks, _ := scanBlocks(lines)
	bMatches := matchSrcBlockLines(srcBlocks, closedBlocks)

	var nodes []*symbolNode
	for i, h := range headings {
		start := hStarts[i]
		end := endOfSubtree(headingLines, start, h.Level, len(lines))
		nodes = append(nodes, &symbolNode{
			level:     h.Level,
			line:      start,
			isHeading: true,
			sym: lsp.DocumentSymbol{
				Name:           headingName(h),
				Detail:         headingDetail(h),
				Range:          lineRange(start, end, lines),
				SelectionRange: lineRange(start, start+1, lines),
			},
		})
	}
	for i, b := range srcBlocks {
		bl := bMatches[i]
		level := enclosingHeadingLevel(headingLines, bl.BeginLine) + 1
		nodes = append(nodes, &symbolNode{
			level: level,
			line:  bl.BeginLine,
			sym: lsp.DocumentSymbol{
				Name:           "src:" + orDefault(b.Lang, "?"),
				Kind:           lsp.SymbolKindConstant,
				Range:          lineRange(bl.BeginLine, bl.EndLine+1, lines),
				SelectionRange: lineRange(bl.BeginLine, bl.BeginLine+1, lines),
			},
		})
	}
	return buildSymbolTree(nodes)
}

// headingName returns h's title, guarding the LSP invariant that a
// DocumentSymbol.Name "must not be an empty string or a string only
// consisting of white spaces" (LSP 3.18 metaModel, DocumentSymbol.name) —
// an org heading can legitimately have no title text (bare "*").
func headingName(h orgast.Heading) string {
	name := strings.TrimSpace(h.Title)
	if name == "" {
		return "(untitled)"
	}
	return name
}

// headingDetail renders a heading's TODO status and tags as supplementary
// detail text, e.g. "TODO :work:urgent:". Either half may be absent.
func headingDetail(h orgast.Heading) string {
	var parts []string
	if h.Node != nil && h.Node.Status != "" {
		parts = append(parts, h.Node.Status)
	}
	if len(h.Tags) > 0 {
		parts = append(parts, ":"+strings.Join(h.Tags, ":")+":")
	}
	return strings.Join(parts, " ")
}

// symbolKindForHeading maps "does this heading have any children" to a
// SymbolKind. LSP has no purpose-built "outline heading" kind, so this is a
// deliberate, documented simplification (mgmt 6131a2a1): Namespace for a
// heading that groups other content (it has at least one child, whether a
// sub-heading or an attached src block), String for a leaf heading — the
// closest sensible stand-in for "a terminal node with only body text".
func symbolKindForHeading(hasChildren bool) lsp.SymbolKind {
	if hasChildren {
		return lsp.SymbolKindNamespace
	}
	return lsp.SymbolKindString
}

// hasChildHeading reports whether headings[i] is immediately followed by a
// heading at a deeper level (i.e. it has at least one sub-heading). Used
// only where no merged-tree context exists (workspace/symbol's flat scan);
// [buildSymbolTree] instead derives Kind from the final rendered children,
// so a heading whose only child is a src block still renders as Namespace.
func hasChildHeading(headings []orgast.Heading, i int) bool {
	return i+1 < len(headings) && headings[i+1].Level > headings[i].Level
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// lineRange returns the Range spanning source lines [startLine, endLine)
// (0-based, endLine exclusive). If endLine reaches past the last line (an
// EOF-bounded span), the Range's end is clamped to the true end of the last
// line instead of naming a line that doesn't exist.
func lineRange(startLine, endLine int, lines []string) lsp.Range {
	last := len(lines) - 1
	if last < 0 {
		last = 0
	}
	if endLine > last {
		endChar := 0
		if last < len(lines) {
			endChar = utf16Len(lines[last])
		}
		return lsp.Range{
			Start: lsp.Position{Line: startLine, Character: 0},
			End:   lsp.Position{Line: last, Character: endChar},
		}
	}
	return lsp.Range{
		Start: lsp.Position{Line: startLine, Character: 0},
		End:   lsp.Position{Line: endLine, Character: 0},
	}
}

// buildSymbolTree sorts nodes by source line and nests them by level using a
// single ancestor stack: while the stack's top has a level >= the current
// node's level, it is popped (it can't be an ancestor); what remains on top,
// if anything, is the current node's parent. This is the same rule for both
// heading nodes and src-block nodes (a src block's level is always its
// enclosing heading's level + 1), so the two interleave naturally in
// document order.
func buildSymbolTree(nodes []*symbolNode) []lsp.DocumentSymbol {
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].line < nodes[j].line })
	var roots []*symbolNode
	var stack []*symbolNode
	for _, n := range nodes {
		for len(stack) > 0 && stack[len(stack)-1].level >= n.level {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, n)
		} else {
			parent := stack[len(stack)-1]
			parent.children = append(parent.children, n)
		}
		stack = append(stack, n)
	}
	return renderSymbolTree(roots)
}

func renderSymbolTree(nodes []*symbolNode) []lsp.DocumentSymbol {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]lsp.DocumentSymbol, len(nodes))
	for i, n := range nodes {
		sym := n.sym
		sym.Children = renderSymbolTree(n.children)
		if n.isHeading {
			sym.Kind = symbolKindForHeading(len(sym.Children) > 0)
		}
		out[i] = sym
	}
	return out
}
