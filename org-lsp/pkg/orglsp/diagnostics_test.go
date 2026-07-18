package orglsp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

func TestDiagnosticsBrokenFileLink(t *testing.T) {
	dir := t.TempDir()
	docPath := filepath.Join(dir, "doc.org")
	// existing.png is real; missing.png is not.
	if err := os.WriteFile(filepath.Join(dir, "existing.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	text := "* Heading\n[[file:existing.png]]\n[[file:missing.png]]\n"

	diags := Diagnostics(docPath, text)
	if len(diags) != 1 {
		t.Fatalf("Diagnostics = %+v, want exactly 1 (missing.png)", diags)
	}
	d := diags[0]
	if d.Severity != lsp.SeverityWarning {
		t.Errorf("Severity = %v, want Warning", d.Severity)
	}
	if got := d.Range.Start.Line; got != 2 {
		t.Errorf("Range.Start.Line = %d, want 2 (the [[file:missing.png]] line)", got)
	}
	if d.Source != lintSource {
		t.Errorf("Source = %q, want %q", d.Source, lintSource)
	}
}

func TestDiagnosticsFileLinkWithSearchSuffix(t *testing.T) {
	dir := t.TempDir()
	docPath := filepath.Join(dir, "doc.org")
	target := filepath.Join(dir, "other.org")
	if err := os.WriteFile(target, []byte("* X\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text := "[[file:other.org::*X]]\n"
	if diags := Diagnostics(docPath, text); len(diags) != 0 {
		t.Errorf("Diagnostics = %+v, want none (search-suffix stripped before existence check)", diags)
	}
}

func TestDiagnosticsAbsoluteFileLink(t *testing.T) {
	dir := t.TempDir()
	docPath := filepath.Join(dir, "doc.org")
	text := "[[file:/definitely/not/a/real/path/xyz.png]]\n"
	diags := Diagnostics(docPath, text)
	if len(diags) != 1 {
		t.Fatalf("Diagnostics = %+v, want exactly 1", diags)
	}
}

func TestDiagnosticsIgnoresNonFileLinks(t *testing.T) {
	text := "[[https://example.com/nope][a web link]]\n[[id:some-node-id]]\n"
	if diags := Diagnostics("doc.org", text); len(diags) != 0 {
		t.Errorf("Diagnostics = %+v, want none (non-file protocols are not checked)", diags)
	}
}

func TestDiagnosticsUnclosedBlock(t *testing.T) {
	text := "* H\n#+begin_src go\nfmt.Println(1)\n"
	diags := Diagnostics("doc.org", text)
	if len(diags) != 1 {
		t.Fatalf("Diagnostics = %+v, want exactly 1 (unclosed block)", diags)
	}
	d := diags[0]
	if d.Severity != lsp.SeverityError {
		t.Errorf("Severity = %v, want Error", d.Severity)
	}
	if d.Range.Start.Line != 1 {
		t.Errorf("Range.Start.Line = %d, want 1 (the #+begin_src line)", d.Range.Start.Line)
	}
}

func TestDiagnosticsClosedBlockIsClean(t *testing.T) {
	text := "#+begin_src go\nfmt.Println(1)\n#+end_src\n"
	if diags := Diagnostics("doc.org", text); len(diags) != 0 {
		t.Errorf("Diagnostics = %+v, want none (block is closed)", diags)
	}
}

func TestDiagnosticsCleanDocument(t *testing.T) {
	text := readFixture(t)
	// The fixture references no [[file:...]] links and has no unclosed
	// blocks, so it must be fully clean.
	if diags := Diagnostics(fixturePath, text); len(diags) != 0 {
		t.Errorf("Diagnostics(golden fixture) = %+v, want none", diags)
	}
}
