package wsedit

import (
	"bytes"
	"fmt"
	"net/url"
	"sort"
	"unicode/utf8"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// FromWorkspaceEdit converts an LSP [lsp.WorkspaceEdit] into a neutral,
// byte-offset [Plan]. It is the only function in this package aware of LSP:
// it performs the two LSP-specific translations the write path needs —
// file:// URI → filesystem path, and (0-indexed line, UTF-16 code-unit
// character) → UTF-8 byte offset.
//
// Byte-offset conversion is content-dependent: an LSP Position addresses a
// line and a UTF-16 code unit within it, neither of which maps to a byte
// offset without the file's actual bytes. Callers therefore supply the source
// bytes of every edited document in sources, keyed by the same URI used in the
// WorkspaceEdit. A URI present in the edit but absent from sources is an error
// (the plan cannot be addressed against unknown content).
//
// The returned Plan's FileEdits are sorted by Path, and each FileEdit's Edits
// are sorted descending by Start (the apply-safe order). Overlapping edits
// within a single file are rejected.
func FromWorkspaceEdit(we lsp.WorkspaceEdit, sources map[lsp.DocumentURI][]byte) (*Plan, error) {
	plan := &Plan{}
	for uri, textEdits := range we.Changes {
		src, ok := sources[uri]
		if !ok {
			return nil, fmt.Errorf("wsedit: no source bytes supplied for %q", uri)
		}
		path, err := URIToPath(uri)
		if err != nil {
			return nil, fmt.Errorf("wsedit: %w", err)
		}

		edits := make([]Edit, 0, len(textEdits))
		for i, te := range textEdits {
			start, err := ByteOffset(src, te.Range.Start)
			if err != nil {
				return nil, fmt.Errorf("wsedit: %s: edit %d start: %w", path, i, err)
			}
			end, err := ByteOffset(src, te.Range.End)
			if err != nil {
				return nil, fmt.Errorf("wsedit: %s: edit %d end: %w", path, i, err)
			}
			if end < start {
				return nil, fmt.Errorf("wsedit: %s: edit %d has end offset %d before start offset %d", path, i, end, start)
			}
			edits = append(edits, Edit{Start: start, End: end, NewText: te.NewText})
		}

		sortDescending(edits)
		if err := checkNonOverlapping(path, edits); err != nil {
			return nil, err
		}

		plan.Files = append(plan.Files, FileEdit{Path: path, Edits: edits})
	}

	sort.Slice(plan.Files, func(i, j int) bool {
		return plan.Files[i].Path < plan.Files[j].Path
	})
	return plan, nil
}

// checkNonOverlapping verifies that edits (already sorted descending by Start)
// do not overlap. Adjacent edits are permitted to touch (edit[i].End ==
// edit[i-1].Start), as are multiple zero-width insertions at the same offset.
func checkNonOverlapping(path string, edits []Edit) error {
	for i := 1; i < len(edits); i++ {
		if edits[i].End > edits[i-1].Start {
			return fmt.Errorf("wsedit: %s: overlapping edits ([%d,%d) and [%d,%d))",
				path, edits[i].Start, edits[i].End, edits[i-1].Start, edits[i-1].End)
		}
	}
	return nil
}

// URIToPath converts an LSP "file://" document URI to an absolute filesystem
// path, percent-decoding the path component. A URI with no scheme is treated
// as an already-decoded path (lenient fallback). Any non-file scheme is an
// error — the write path only edits local files.
func URIToPath(uri lsp.DocumentURI) (string, error) {
	s := string(uri)
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("parse URI %q: %w", s, err)
	}
	switch u.Scheme {
	case "file":
		// url.Path is already percent-decoded. For a local file the authority
		// (host) is empty: file:///abs/path → u.Host=="" , u.Path=="/abs/path".
		if u.Path == "" {
			return "", fmt.Errorf("file URI %q has no path", s)
		}
		return u.Path, nil
	case "":
		// No scheme: treat the raw string as a path. Still percent-decode to
		// be forgiving of callers that hand-encode.
		if dec, derr := url.PathUnescape(s); derr == nil {
			return dec, nil
		}
		return s, nil
	default:
		return "", fmt.Errorf("unsupported URI scheme %q in %q (write path edits local files only)", u.Scheme, s)
	}
}

// ByteOffset maps an LSP [lsp.Position] (0-indexed line; character measured in
// UTF-16 code units) to a byte offset into the UTF-8 source src.
//
// It walks src to the start of pos.Line (splitting on '\n'; a trailing "\r" of
// a "\r\n" ending is treated as belonging to the line terminator, not the
// line's text), then advances within that line counting UTF-16 code units — 1
// per BMP rune, 2 per rune above U+FFFF (a surrogate pair) — until pos.Character
// units are consumed.
//
// Clamping semantics (matching common LSP server/client behaviour):
//   - A Character past the end of the line clamps to the line's end (the
//     offset just before its '\n', or EOF for the final line).
//   - A Character that would land in the middle of a surrogate pair clamps to
//     the start of that rune (offsets never split a rune).
//
// A Line beyond the last line of src is an error (a genuine out-of-range
// position, typically a staleness signal). Line == number-of-newlines is
// valid and addresses the empty virtual line at EOF (used to append).
func ByteOffset(src []byte, pos lsp.Position) (int, error) {
	if pos.Line < 0 || pos.Character < 0 {
		return 0, fmt.Errorf("negative position line=%d character=%d", pos.Line, pos.Character)
	}

	// 1. Advance to the start-of-line byte offset for pos.Line.
	lineStart := 0
	line := 0
	for line < pos.Line {
		nl := indexByte(src, lineStart, '\n')
		if nl < 0 {
			return 0, fmt.Errorf("line %d beyond end of %d-line document", pos.Line, line+1)
		}
		lineStart = nl + 1
		line++
	}

	// 2. Advance within the line by pos.Character UTF-16 code units.
	off := lineStart
	units := 0
	for off < len(src) && units < pos.Character {
		r, size := utf8.DecodeRune(src[off:])
		if r == '\n' {
			break // Character past end of line → clamp to line end.
		}
		if r == '\r' && off+1 < len(src) && src[off+1] == '\n' {
			break // "\r" of a "\r\n" ending is not line text.
		}
		w := 1
		if r > 0xFFFF {
			w = 2 // rune encodes as a UTF-16 surrogate pair.
		}
		if units+w > pos.Character {
			break // would land mid-surrogate → clamp to this rune's start.
		}
		off += size
		units += w
	}
	return off, nil
}

// indexByte returns the index of the first b at or after start in src, or -1.
func indexByte(src []byte, start int, b byte) int {
	if start >= len(src) {
		return -1
	}
	i := bytes.IndexByte(src[start:], b)
	if i < 0 {
		return -1
	}
	return start + i
}
