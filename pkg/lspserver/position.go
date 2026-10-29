package lspserver

import (
	"unicode/utf8"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// Point is a tree-sitter style position: zero-based row and a BYTE column.
type Point struct{ Row, Col int }

// LineStarts returns the byte offset of the start of every line of text (lines
// end at '\n'; the final, possibly empty, line is included).
func LineStarts(text []byte) []int {
	starts := []int{0}
	for i, b := range text {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// OffsetAt maps a UTF-16 LSP position to a byte offset into text.
//
// Per the spec, a line beyond the last line clamps to the end of the document,
// and a character beyond the end of its line clamps to the line end (before a
// "\r\n" or "\n" terminator). A character that lands inside a surrogate pair
// rounds down to the start of that rune, so an offset never splits a rune.
func OffsetAt(text []byte, pos lsp.Position) int {
	return offsetAt(text, LineStarts(text), pos)
}

func offsetAt(text []byte, starts []int, pos lsp.Position) int {
	if pos.Line < 0 {
		return 0
	}
	if pos.Line >= len(starts) {
		return len(text)
	}
	off := starts[pos.Line]
	end := len(text)
	if pos.Line+1 < len(starts) {
		end = starts[pos.Line+1] - 1 // exclude '\n'
		if end > off && text[end-1] == '\r' {
			end--
		}
	}
	units := 0
	for off < end && units < pos.Character {
		r, size := utf8.DecodeRune(text[off:end])
		w := 1
		if r > 0xFFFF {
			w = 2
		}
		if units+w > pos.Character {
			break
		}
		off += size
		units += w
	}
	return off
}

// PositionAt maps a byte offset into text to a UTF-16 LSP position. An offset
// outside [0, len(text)] is clamped; an offset inside a multi-byte rune
// reports the position of that rune's start.
func PositionAt(text []byte, off int) lsp.Position {
	return positionAt(text, LineStarts(text), off)
}

func positionAt(text []byte, starts []int, off int) lsp.Position {
	if off < 0 {
		off = 0
	}
	if off > len(text) {
		off = len(text)
	}
	for off > 0 && off < len(text) && !utf8.RuneStart(text[off]) {
		off-- // inside a multi-byte rune: report the rune's start
	}
	// Largest line whose start <= off.
	lo, hi := 0, len(starts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if starts[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lsp.Position{Line: lo, Character: UTF16Len(text[starts[lo]:off])}
}

// UTF16Len is the number of UTF-16 code units in the UTF-8 bytes b: one per
// rune in the BMP and two (a surrogate pair) per rune above U+FFFF. Invalid
// bytes count as one unit each, matching how utf8.DecodeRune steps over them.
func UTF16Len(b []byte) int {
	n := 0
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
		b = b[size:]
	}
	return n
}

// PointAt is the tree-sitter point (row, byte column) of byte offset off.
func PointAt(text []byte, starts []int, off int) Point {
	if off < 0 {
		off = 0
	}
	if off > len(text) {
		off = len(text)
	}
	lo, hi := 0, len(starts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if starts[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return Point{Row: lo, Col: off - starts[lo]}
}
