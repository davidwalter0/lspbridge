// Package orglsp implements the org-lsp language server: an
// [github.com/davidwalter0/lspbridge/pkg/jsonrpc.Handler] that answers a
// small, honest subset of the Language Server Protocol for org-mode text
// documents. See [Server] for the method surface and doc.go (via the
// org-lsp/README.org) for the full capability/non-goal list.
//
// # Why this file exists
//
// The engine, [github.com/davidwalter0/org/orgast], wraps
// niklasfasching/go-org v1.9.1. That AST carries no source positions at
// all — no byte offset, no line number, not even on org.Headline — because
// go-org was built for rendering (HTML/plain-org export), not for tooling
// that must answer "where in the buffer is this." LSP is fundamentally
// position-based (every Range is a line/character span), so org-lsp cannot
// get Ranges from orgast directly.
//
// This file is the position oracle that fills that gap. It re-scans the raw
// document text with small, line-oriented regexes (heading markers, #+begin/
// #+end block pairs, :NAME:/:END: drawer pairs — the same syntactic tokens
// go-org's own lexer recognizes, see org-syntax.html) and then *aligns* that
// scan against orgast's already crypt/PGP-filtered semantic lists
// (Headings, SrcBlocks, Links) by walking both in document order and
// matching on (level, text-containment) rather than by index. That means:
//
//   - orgast still owns all *semantics* (title text, tags, properties,
//     language, crypt/PGP filtering) — this file never re-derives any of
//     that.
//   - This file only owns *positions*, and it never needs to understand
//     :crypt: or PGP-armor itself: because orgast's lists already exclude
//     crypt-subtree descendants and PGP blocks, a forward-only, in-order
//     search naturally steps over the corresponding raw lines without this
//     file ever inspecting a tag.
//   - Alignment degrades gracefully (never panics, never misaligns *later*
//     entries because of one bad match) if a title's rendered form doesn't
//     literally appear in the source line (rare — e.g. exotic inline
//     markup) — see the fallback tiers on [matchHeadingStarts].
package orglsp

import (
	"regexp"
	"strings"

	"github.com/davidwalter0/org/orgast"
)

// splitLines splits text into its lines without the trailing "\n" (a final
// unterminated line, or an empty trailing line for a "\n"-terminated file,
// is preserved as its own element — that mirrors LSP's own line counting,
// where line N exists whether or not it ends with a newline).
func splitLines(text string) []string {
	return strings.Split(text, "\n")
}

// utf16Len returns the UTF-16 code-unit length of s, per the LSP Position
// character-offset semantics (see [github.com/davidwalter0/lspbridge/pkg/lsp.Position]).
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// HeadingLine is one raw heading-syntax line ("*+ text"), found by a direct
// scan of the source — unfiltered: it includes headings inside a :crypt:
// subtree, because subtree-extent computation ([endOfSubtree]) needs the
// true physical structure regardless of crypt filtering (see the package
// doc's second bullet).
type HeadingLine struct {
	Level int    // number of leading '*' characters
	Line  int    // 0-based source line number
	Text  string // line content after the level markers and one run of whitespace
}

var headingLineRe = regexp.MustCompile(`^(\*+)\s+(.*)$`)

// scanHeadingLines finds every heading-syntax line in lines, in order.
func scanHeadingLines(lines []string) []HeadingLine {
	var out []HeadingLine
	for i, raw := range lines {
		m := headingLineRe.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		out = append(out, HeadingLine{Level: len(m[1]), Line: i, Text: m[2]})
	}
	return out
}

// matchHeadingStarts aligns headings (orgast's flat, in-order, crypt/PGP-
// filtered heading list) against candidateLines (every heading-syntax line
// in the raw source) and returns, for each headings[i], its 0-based start
// line. Matching advances a cursor through candidateLines so headings are
// matched strictly in order and a line is never reused; candidate lines
// that don't correspond to any entry in headings (crypt-subtree
// descendants) are simply stepped over without ever being recognized as
// such — see the package doc.
//
// Matching tries, per heading, in order:
//  1. the next candidate at the same Level whose Text contains the
//     heading's Title (the common case: title text round-trips into the
//     line almost verbatim, since TODO/priority/tag decorations only ever
//     add a prefix or suffix around it);
//  2. the next candidate at the same Level at all (title text didn't
//     literally match — e.g. reformatted inline markup);
//  3. the cursor position itself, clamped into range (last resort: this
//     never panics and never desyncs later headings any worse than the
//     one already-ambiguous entry).
func matchHeadingStarts(headings []orgast.Heading, candidateLines []HeadingLine) []int {
	starts := make([]int, len(headings))
	cursor := 0
	for i, h := range headings {
		idx := findHeadingFrom(candidateLines, cursor, h.Level, h.Title)
		if idx < 0 {
			idx = findLevelFrom(candidateLines, cursor, h.Level)
		}
		switch {
		case idx >= 0:
			starts[i] = candidateLines[idx].Line
			cursor = idx + 1
		case cursor < len(candidateLines):
			starts[i] = candidateLines[cursor].Line
		case len(candidateLines) > 0:
			starts[i] = candidateLines[len(candidateLines)-1].Line
		default:
			starts[i] = 0
		}
	}
	return starts
}

func findHeadingFrom(lines []HeadingLine, from, level int, title string) int {
	title = strings.TrimSpace(title)
	for i := from; i < len(lines); i++ {
		if lines[i].Level != level {
			continue
		}
		if title == "" || strings.Contains(lines[i].Text, title) {
			return i
		}
	}
	return -1
}

func findLevelFrom(lines []HeadingLine, from, level int) int {
	for i := from; i < len(lines); i++ {
		if lines[i].Level == level {
			return i
		}
	}
	return -1
}

// endOfSubtree returns the 0-based, EXCLUSIVE end line for a heading that
// starts at startLine with the given level: the line of the next
// heading-syntax line at level <= level (using the unfiltered
// candidateLines, so a subtree's physical extent is correct even across a
// nested :crypt: child), or totalLines if there is none.
func endOfSubtree(candidateLines []HeadingLine, startLine, level, totalLines int) int {
	for _, hl := range candidateLines {
		if hl.Line > startLine && hl.Level <= level {
			return hl.Line
		}
	}
	return totalLines
}

// enclosingHeadingLevel returns the Level of the last heading-syntax line
// strictly before beforeLine, or 0 if beforeLine precedes every heading
// (i.e. it's file-level content).
func enclosingHeadingLevel(candidateLines []HeadingLine, beforeLine int) int {
	level := 0
	for _, hl := range candidateLines {
		if hl.Line >= beforeLine {
			break
		}
		level = hl.Level
	}
	return level
}

// BlockLine is one closed #+begin_NAME/#+end_NAME span, found by a direct
// scan of the source.
type BlockLine struct {
	Name      string // upper-cased block name, e.g. "SRC", "EXAMPLE"
	Params    string // raw text after the name on the #+begin_ line
	BeginLine int    // 0-based line of "#+begin_NAME ..."
	EndLine   int    // 0-based line of the matching "#+end_NAME"
}

// UnclosedBlock is a #+begin_NAME with no matching #+end_NAME before EOF.
type UnclosedBlock struct {
	Name string
	Line int // 0-based line of the "#+begin_NAME"
}

var beginBlockRe = regexp.MustCompile(`(?i)^\s*#\+begin_(\S+)(.*)$`)
var endBlockRe = regexp.MustCompile(`(?i)^\s*#\+end_(\S+)\s*$`)

// scanBlocks finds every #+begin_/#+end_ block in lines. Blocks are matched
// LIFO by name (mirroring go-org's own parser): an #+end_X closes the
// nearest still-open #+begin_X, so this also naturally handles the (rare,
// arguably invalid) case of a block nested inside a different block. Any
// #+begin_ left on the stack at EOF is reported as unclosed rather than
// silently dropped — this is exactly what the diagnostics pass wants.
func scanBlocks(lines []string) (closed []BlockLine, unclosed []UnclosedBlock) {
	type open struct {
		name, params string
		line         int
	}
	var stack []open
	for i, raw := range lines {
		if m := beginBlockRe.FindStringSubmatch(raw); m != nil {
			stack = append(stack, open{name: strings.ToUpper(m[1]), params: strings.TrimSpace(m[2]), line: i})
			continue
		}
		if m := endBlockRe.FindStringSubmatch(raw); m != nil {
			name := strings.ToUpper(m[1])
			for j := len(stack) - 1; j >= 0; j-- {
				if stack[j].name == name {
					closed = append(closed, BlockLine{Name: name, Params: stack[j].params, BeginLine: stack[j].line, EndLine: i})
					stack = append(stack[:j], stack[j+1:]...)
					break
				}
			}
		}
	}
	for _, o := range stack {
		unclosed = append(unclosed, UnclosedBlock{Name: o.name, Line: o.line})
	}
	return closed, unclosed
}

// matchSrcBlockLines aligns srcBlocks (orgast's flat, in-order, crypt/PGP-
// filtered SRC block list) against candidates (every closed block in the raw
// source, of every block type) using the same in-order, never-reuse cursor
// strategy as [matchHeadingStarts]. A block with no match at all yields the
// zero [BlockLine] (BeginLine/EndLine both 0) — callers must treat that as
// "position unknown" rather than a real line 0 span; this is an accepted,
// rare degradation (see the package doc), not a crash.
func matchSrcBlockLines(srcBlocks []orgast.SrcBlock, candidates []BlockLine) []BlockLine {
	result := make([]BlockLine, len(srcBlocks))
	cursor := 0
	for i, b := range srcBlocks {
		idx := findSrcFrom(candidates, cursor, b.Lang)
		if idx < 0 {
			idx = findNameFrom(candidates, cursor, "SRC")
		}
		if idx >= 0 {
			result[i] = candidates[idx]
			cursor = idx + 1
		}
	}
	return result
}

func findSrcFrom(candidates []BlockLine, from int, lang string) int {
	lang = strings.ToLower(strings.TrimSpace(lang))
	for i := from; i < len(candidates); i++ {
		if candidates[i].Name != "SRC" {
			continue
		}
		if lang == "" || strings.HasPrefix(strings.ToLower(candidates[i].Params), lang) {
			return i
		}
	}
	return -1
}

func findNameFrom(candidates []BlockLine, from int, name string) int {
	for i := from; i < len(candidates); i++ {
		if candidates[i].Name == name {
			return i
		}
	}
	return -1
}

// drawerSpan is one :NAME:/:END: drawer span, found by a direct scan of the
// source. orgast has no typed Drawer projection (see its package doc's
// scope-discipline note), so unlike headings/blocks there is no semantic
// list to align against here — a drawerSpan's Name is purely for debugging.
type drawerSpan struct {
	Name      string
	BeginLine int
	EndLine   int
}

var beginDrawerRe = regexp.MustCompile(`^\s*:(\S+):\s*$`)
var endDrawerRe = regexp.MustCompile(`(?i)^\s*:END:\s*$`)

// scanDrawers finds every :NAME:/:END: drawer span in lines. Like
// [scanBlocks], drawers close LIFO — but unlike blocks, org-syntax drawers
// close on a bare ":END:" regardless of the opening name, so the end marker
// carries no name to match against; the most recently opened drawer always
// closes first.
func scanDrawers(lines []string) []drawerSpan {
	var out []drawerSpan
	var stack []drawerSpan
	for i, raw := range lines {
		if endDrawerRe.MatchString(raw) {
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				top.EndLine = i
				out = append(out, top)
			}
			continue
		}
		if m := beginDrawerRe.FindStringSubmatch(raw); m != nil {
			stack = append(stack, drawerSpan{Name: strings.ToUpper(m[1]), BeginLine: i})
		}
	}
	return out
}

// findLineContaining returns the index of the first line at or after from
// that contains needle, or -1 if none does.
func findLineContaining(lines []string, from int, needle string) int {
	if needle == "" {
		return -1
	}
	for i := from; i < len(lines); i++ {
		if strings.Contains(lines[i], needle) {
			return i
		}
	}
	return -1
}
