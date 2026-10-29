package gotmpl

import (
	"bytes"

	"github.com/davidwalter0/gotreesitter"
)

// actionSpans returns the byte spans of every {{ ... }} action, found
// lexically: it honours the quoting forms of Go templates ("..." with
// escapes, `...` raw strings, '...' runes) and /* ... */ comments, so a "}}"
// inside a string does not end the action. An action with no closing "}}"
// is spanned to the end of its opening line, which keeps one broken action
// from swallowing the rest of the document.
func actionSpans(src []byte) [][2]int {
	var spans [][2]int
	i := 0
	for {
		j := bytes.Index(src[i:], []byte("{{"))
		if j < 0 {
			return spans
		}
		start := i + j
		end := scanActionEnd(src, start+2)
		if end < 0 {
			end = lineEnd(src, start)
		}
		spans = append(spans, [2]int{start, end})
		i = end
	}
}

// scanActionEnd returns the offset just past the "}}" that closes an action
// whose body begins at p, or -1 when there is none.
func scanActionEnd(src []byte, p int) int {
	for p < len(src) {
		switch c := src[p]; {
		case c == '}' && p+1 < len(src) && src[p+1] == '}':
			return p + 2
		case c == '"' || c == '\'':
			p++
			for p < len(src) && src[p] != c && src[p] != '\n' {
				if src[p] == '\\' {
					p++
				}
				p++
			}
			p++ // closing quote (or newline: an unterminated literal)
		case c == '`':
			k := bytes.IndexByte(src[p+1:], '`')
			if k < 0 {
				return -1
			}
			p += k + 2
		case c == '/' && p+1 < len(src) && src[p+1] == '*':
			k := bytes.Index(src[p+2:], []byte("*/"))
			if k < 0 {
				return -1
			}
			p += k + 4
		default:
			p++
		}
	}
	return -1
}

func lineEnd(src []byte, from int) int {
	if k := bytes.IndexByte(src[from:], '\n'); k >= 0 {
		return from + k
	}
	return len(src)
}

// BlankActions returns a copy of src with every {{ ... }} action replaced by
// spaces of equal byte length. Newlines inside an action are kept, so byte
// offsets, lines and columns of the surrounding text are unchanged. The
// result is what a YAML parser should see in place of a helm template.
func BlankActions(src []byte) []byte {
	out := make([]byte, len(src))
	copy(out, src)
	for _, s := range actionSpans(src) {
		for k := s[0]; k < s[1]; k++ {
			if out[k] != '\n' {
				out[k] = ' '
			}
		}
	}
	return out
}

// YAMLRanges returns the raw-text (non-action) ranges of a parsed template, in
// document order, for gotreesitter.Parser.SetIncludedRanges: parsing the
// source with these ranges makes a YAML parser see only the text between
// actions. Action extents come from the delimiter tokens of the tree; an
// opening delimiter with no (real) closing delimiter, as in an unclosed
// "{{ if .Values.x }", excludes the rest of its line. A nil or empty tree
// yields the whole source as one range.
func YAMLRanges(tree *gotreesitter.Tree, src []byte) []gotreesitter.Range {
	if len(src) == 0 {
		return nil
	}
	var spans [][2]int
	if tree != nil && tree.RootNode() != nil {
		spans = treeActionSpans(tree, src)
	}
	return complement(spans, src)
}

// treeActionSpans pairs the "{{" / "{{-" and "}}" / "-}}" tokens of the tree,
// in document order, into action spans.
func treeActionSpans(tree *gotreesitter.Tree, src []byte) [][2]int {
	lang := tree.Language()
	var spans [][2]int
	open := -1
	flush := func() { // the open action never closed: it ends with its line
		if open >= 0 {
			spans = append(spans, [2]int{open, lineEnd(src, open)})
			open = -1
		}
	}
	var walk func(n *gotreesitter.Node)
	walk = func(n *gotreesitter.Node) {
		if n.ChildCount() == 0 {
			switch n.Type(lang) {
			case "{{", "{{-":
				if n.IsMissing() {
					return
				}
				flush()
				open = int(n.StartByte())
			case "}}", "-}}":
				if n.IsMissing() {
					return // a recovery-inserted close is not a real one
				}
				if open >= 0 {
					spans = append(spans, [2]int{open, int(n.EndByte())})
					open = -1
				}
			}
			return
		}
		for i := 0; i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(tree.RootNode())
	flush()
	return spans
}

// complement turns sorted, non-overlapping action spans into the ranges
// between them.
func complement(spans [][2]int, src []byte) []gotreesitter.Range {
	var out []gotreesitter.Range
	pos := 0
	add := func(a, b int) {
		if b > a {
			out = append(out, gotreesitter.Range{
				StartByte:  uint32(a),
				EndByte:    uint32(b),
				StartPoint: pointAt(src, a),
				EndPoint:   pointAt(src, b),
			})
		}
	}
	for _, s := range spans {
		if s[0] < pos {
			continue
		}
		add(pos, s[0])
		pos = s[1]
	}
	add(pos, len(src))
	return out
}

func pointAt(src []byte, off int) gotreesitter.Point {
	row := bytes.Count(src[:off], []byte{'\n'})
	col := off - (bytes.LastIndexByte(src[:off], '\n') + 1)
	return gotreesitter.Point{Row: uint32(row), Column: uint32(col)}
}
