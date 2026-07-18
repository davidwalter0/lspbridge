package orglsp

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeOrgFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceIndexSymbolsMatchAndSkipHidden(t *testing.T) {
	root := t.TempDir()
	writeOrgFile(t, filepath.Join(root, "a.org"), "* Alpha Project\n** Alpha Sub\n")
	writeOrgFile(t, filepath.Join(root, "sub", "b.org"), "* Beta Project\n")
	writeOrgFile(t, filepath.Join(root, ".git", "hidden.org"), "* Should Never Match\n")
	writeOrgFile(t, filepath.Join(root, "notes.md"), "# not an org file\n")

	idx := NewWorkspaceIndex(root)

	syms, err := idx.Symbols("alpha")
	if err != nil {
		t.Fatalf("Symbols: %v", err)
	}
	if len(syms) != 2 {
		t.Fatalf("Symbols(alpha) = %+v, want 2 (case-insensitive substring match on both Alpha headings)", syms)
	}

	all, err := idx.Symbols("")
	if err != nil {
		t.Fatalf("Symbols(\"\"): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("Symbols(\"\") = %+v, want 3 (Alpha Project, Alpha Sub, Beta Project — NOT the hidden-dir heading)", all)
	}
	for _, s := range all {
		if s.Name == "Should Never Match" {
			t.Errorf("hidden-directory file was scanned: %+v", s)
		}
	}

	none, err := idx.Symbols("nonexistent-query-xyz")
	if err != nil {
		t.Fatalf("Symbols(no-match): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("Symbols(no-match) = %+v, want none", none)
	}
}

func TestWorkspaceIndexSymbolLocation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.org")
	writeOrgFile(t, path, "prose\n\n* Target Heading\nbody\n")

	idx := NewWorkspaceIndex(root)
	syms, err := idx.Symbols("target")
	if err != nil {
		t.Fatalf("Symbols: %v", err)
	}
	if len(syms) != 1 {
		t.Fatalf("Symbols(target) = %+v, want 1", syms)
	}
	want := pathToURI(path)
	if syms[0].Location.URI != want {
		t.Errorf("Location.URI = %q, want %q", syms[0].Location.URI, want)
	}
	if syms[0].Location.Range.Start.Line != 2 {
		t.Errorf("Location.Range.Start.Line = %d, want 2", syms[0].Location.Range.Start.Line)
	}
}

// TestWorkspaceIndexCachesByMTime proves the "cache per-file by mtime"
// requirement: with the mtime pinned unchanged across a content rewrite, a
// second query must return the STALE (cached) result; bumping the mtime
// must invalidate the cache and pick up the new content. Pinning mtime via
// os.Chtimes (rather than relying on two real-time writes landing in the
// same filesystem-timestamp tick) makes this deterministic.
func TestWorkspaceIndexCachesByMTime(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.org")
	writeOrgFile(t, path, "* Original Heading\n")

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	pinned := st.ModTime()

	idx := NewWorkspaceIndex(root)
	first, err := idx.Symbols("")
	if err != nil {
		t.Fatalf("Symbols (first): %v", err)
	}
	if len(first) != 1 || first[0].Name != "Original Heading" {
		t.Fatalf("first Symbols = %+v", first)
	}

	// Rewrite with different content, then force the mtime back to the
	// exact value already cached.
	writeOrgFile(t, path, "* Changed Heading\n")
	if err := os.Chtimes(path, pinned, pinned); err != nil {
		t.Fatal(err)
	}

	second, err := idx.Symbols("")
	if err != nil {
		t.Fatalf("Symbols (second, same mtime): %v", err)
	}
	if len(second) != 1 || second[0].Name != "Original Heading" {
		t.Fatalf("second Symbols = %+v, want the STALE cached entry (mtime unchanged)", second)
	}

	// Now bump the mtime forward: the cache must invalidate.
	bumped := pinned.Add(time.Second)
	if err := os.Chtimes(path, bumped, bumped); err != nil {
		t.Fatal(err)
	}
	third, err := idx.Symbols("")
	if err != nil {
		t.Fatalf("Symbols (third, bumped mtime): %v", err)
	}
	if len(third) != 1 || third[0].Name != "Changed Heading" {
		t.Fatalf("third Symbols = %+v, want the fresh entry after mtime bump", third)
	}
}

func TestWorkspaceIndexSkipsUnparsableFile(t *testing.T) {
	root := t.TempDir()
	writeOrgFile(t, filepath.Join(root, "a.org"), "* Fine Heading\n")
	// A directory named *.org must not crash the scan; WalkDir simply won't
	// treat it as a file to index (IsDir() short-circuits first).
	if err := os.MkdirAll(filepath.Join(root, "weird.org"), 0o755); err != nil {
		t.Fatal(err)
	}
	idx := NewWorkspaceIndex(root)
	syms, err := idx.Symbols("")
	if err != nil {
		t.Fatalf("Symbols: %v", err)
	}
	if len(syms) != 1 || syms[0].Name != "Fine Heading" {
		t.Fatalf("Symbols = %+v", syms)
	}
}

func TestWorkspaceIndexNoRoot(t *testing.T) {
	idx := NewWorkspaceIndex(filepath.Join(t.TempDir(), "does-not-exist"))
	syms, err := idx.Symbols("")
	if err != nil {
		t.Fatalf("Symbols over a missing root should not error, got: %v", err)
	}
	if len(syms) != 0 {
		t.Errorf("Symbols = %+v, want none", syms)
	}
}
