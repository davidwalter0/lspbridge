// Package wsedit is the WRITE-path seam between an LSP server and ae
// (mcp-agent-editor). An LSP server computes a pure [lsp.WorkspaceEdit]
// (never touching disk); wsedit maps it to a neutral, self-contained edit
// [Plan] addressed by byte offsets; ae adapts that Plan into its own
// transaction.Plan and performs the confirm-gated, atomic apply.
//
// # Dependency direction (architecture decision)
//
// The Plan type here is deliberately NEUTRAL: it imports neither ae nor even
// this repo's own pkg/lsp. The seam points heavy → light: ae (a ~30-dependency
// module requiring a newer Go toolchain, and pulling in a CGO tree-sitter tree
// for its python/rust editors) depends on this contract, NOT the other way
// round. lspbridge stays a pure-standard-library, zero-external-dependency,
// CGO_ENABLED=0 module — importing ae's transaction package would have
// destroyed that invariant even though transaction itself is cgo-free, because
// a single cross-module import drags ae's entire go.mod require-graph and go
// version into lspbridge's go.sum. See docs/design/wsedit-ae-seam.org.
//
// # How ae consumes a Plan
//
// ae's write model represents each file change as a unified diff
// (transaction.FileChange.UnifiedDiff), applied by its strict, staleness-
// checked ApplyUnifiedDiff. The offset-based Plan here is the clean upstream
// of that: for each [FileEdit] ae reads the file's current bytes as "before",
// computes "after" with [FileEdit.Apply], diffs before→after into a unified
// diff (ae already vendors go-difflib), and wraps the result in a
// transaction.Plan for its gated Apply. Keeping the offset→diff translation on
// ae's side means lspbridge never has to reproduce difflib's exact framing.
package wsedit

import (
	"fmt"
	"sort"
)

// Edit is a single replacement in one file, addressed by a half-open byte
// range [Start, End) into the file's original (pre-edit) bytes. Applying it
// replaces those bytes with NewText. A pure insertion has Start == End; a pure
// deletion has NewText == "".
type Edit struct {
	// Start is the inclusive byte offset into the original file bytes.
	Start int
	// End is the exclusive byte offset into the original file bytes.
	End int
	// NewText is the UTF-8 replacement text.
	NewText string
}

// FileEdit is the ordered set of edits for a single file. Edits are held in
// DESCENDING Start order (highest offset first): applied left-to-right in that
// order, each edit's byte range is still valid in the buffer because every
// edit already applied lay strictly after it, so no earlier offset is ever
// invalidated. Within one file the edits are guaranteed non-overlapping (LSP
// requires this, and [FromWorkspaceEdit] enforces it).
type FileEdit struct {
	// Path is the absolute filesystem path (the file:// URI stripped and
	// percent-decoded).
	Path string
	// Edits are the file's replacements, sorted descending by Start.
	Edits []Edit
}

// Plan is a neutral, multi-file edit plan. It is pure data — nothing here
// writes to disk. Files are sorted by Path for deterministic output.
type Plan struct {
	Files []FileEdit
}

// Apply returns src with the FileEdit's edits applied. It is the executable
// statement of the ordering contract: edits are applied in the stored
// descending order so that mutating a later (higher-offset) span never shifts
// an earlier (lower-offset) span's coordinates.
//
// All edits are validated against the ORIGINAL src length and for
// non-overlap before any byte is copied, so a stale offset (End past EOF, or
// two edits that overlap) is reported rather than silently mis-applied. Apply
// never mutates src.
func (fe FileEdit) Apply(src []byte) ([]byte, error) {
	n := len(src)
	for i, e := range fe.Edits {
		if e.Start < 0 || e.End < e.Start || e.End > n {
			return nil, fmt.Errorf("wsedit: %s: edit %d range [%d,%d) out of bounds for %d-byte file", fe.Path, i, e.Start, e.End, n)
		}
		if i > 0 {
			prev := fe.Edits[i-1]
			if e.Start > prev.Start {
				return nil, fmt.Errorf("wsedit: %s: edits not in descending order (edit %d start %d > edit %d start %d)", fe.Path, i, e.Start, i-1, prev.Start)
			}
			if e.End > prev.Start {
				return nil, fmt.Errorf("wsedit: %s: overlapping edits (edit %d ends at %d, edit %d starts at %d)", fe.Path, i, e.End, i-1, prev.Start)
			}
		}
	}

	out := src
	for _, e := range fe.Edits {
		buf := make([]byte, 0, len(out)-(e.End-e.Start)+len(e.NewText))
		buf = append(buf, out[:e.Start]...)
		buf = append(buf, e.NewText...)
		buf = append(buf, out[e.End:]...)
		out = buf
	}
	return out, nil
}

// sortDescending orders edits highest-Start-first, tie-breaking on End
// (highest first) so the ordering is deterministic for equal-Start edits.
func sortDescending(edits []Edit) {
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].Start != edits[j].Start {
			return edits[i].Start > edits[j].Start
		}
		return edits[i].End > edits[j].End
	})
}
