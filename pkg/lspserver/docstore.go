package lspserver

import (
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// Doc is an immutable snapshot of one open document. The store replaces the
// whole value on every change, so a handler holding a *Doc is never affected
// by later edits.
type Doc struct {
	URI        lsp.DocumentURI
	Path       string // filesystem path for file: URIs, else the URI string
	LanguageID string
	Version    int
	Text       []byte

	starts []int // line-start byte offsets, computed once per snapshot
}

func newDoc(uri lsp.DocumentURI, lang string, version int, text []byte) *Doc {
	return &Doc{URI: uri, Path: URIToPath(uri), LanguageID: lang, Version: version, Text: text, starts: LineStarts(text)}
}

// Offset maps a UTF-16 position to a byte offset in this snapshot.
func (d *Doc) Offset(p lsp.Position) int { return offsetAt(d.Text, d.starts, p) }

// Position maps a byte offset in this snapshot to a UTF-16 position.
func (d *Doc) Position(off int) lsp.Position { return positionAt(d.Text, d.starts, off) }

// Point maps a byte offset to a (row, byte column) point.
func (d *Doc) Point(off int) Point { return PointAt(d.Text, d.starts, off) }

// Range converts a byte span to an LSP range.
func (d *Doc) Range(start, end int) lsp.Range {
	return lsp.Range{Start: d.Position(start), End: d.Position(end)}
}

// Edit describes one applied text change in the byte/point terms an
// incremental parser wants. Edits from one didChange are sequential: each is
// expressed against the text left by the one before it.
type Edit struct {
	StartByte, OldEndByte, NewEndByte    int
	StartPoint, OldEndPoint, NewEndPoint Point
}

// Change is the outcome of applying a didChange to the store.
type Change struct {
	Old, New *Doc
	Edits    []Edit // empty when a full-document replacement was applied
	Full     bool   // true if any change replaced the whole document
}

// Store holds the open documents.
type Store struct {
	mu   sync.RWMutex
	docs map[lsp.DocumentURI]*Doc
}

// NewStore returns an empty store.
func NewStore() *Store { return &Store{docs: map[lsp.DocumentURI]*Doc{}} }

// Get returns the current snapshot of uri, or nil.
func (s *Store) Get(uri lsp.DocumentURI) *Doc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.docs[uri]
}

// All returns a snapshot of every open document (unordered).
func (s *Store) All() []*Doc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Doc, 0, len(s.docs))
	for _, d := range s.docs {
		out = append(out, d)
	}
	return out
}

// Open stores a document, replacing any previous one under the same URI.
func (s *Store) Open(item lsp.TextDocumentItem) *Doc {
	d := newDoc(item.URI, item.LanguageID, item.Version, []byte(item.Text))
	s.mu.Lock()
	s.docs[item.URI] = d
	s.mu.Unlock()
	return d
}

// Close forgets uri and returns the last snapshot (nil if it was not open).
func (s *Store) Close(uri lsp.DocumentURI) *Doc {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[uri]
	delete(s.docs, uri)
	return d
}

// Apply applies p's changes in order. It returns nil if the document is not
// open. A change with Range == nil replaces the whole text; otherwise the
// range is resolved against the text as left by the previous change.
func (s *Store) Apply(p lsp.DidChangeTextDocumentParams) *Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.docs[p.TextDocument.URI]
	if old == nil {
		return nil
	}
	text := old.Text
	starts := old.starts
	ch := &Change{Old: old}
	copied := false
	for _, c := range p.ContentChanges {
		if c.Range == nil {
			text = []byte(c.Text)
			starts = LineStarts(text)
			ch.Full = true
			ch.Edits = nil
			copied = true
			continue
		}
		a := offsetAt(text, starts, c.Range.Start)
		b := offsetAt(text, starts, c.Range.End)
		if b < a {
			a, b = b, a
		}
		e := Edit{StartByte: a, OldEndByte: b, NewEndByte: a + len(c.Text),
			StartPoint: PointAt(text, starts, a), OldEndPoint: PointAt(text, starts, b)}
		nt := make([]byte, 0, len(text)-(b-a)+len(c.Text))
		nt = append(nt, text[:a]...)
		nt = append(nt, c.Text...)
		nt = append(nt, text[b:]...)
		text = nt
		starts = LineStarts(text)
		e.NewEndPoint = PointAt(text, starts, e.NewEndByte)
		if !ch.Full {
			ch.Edits = append(ch.Edits, e)
		}
		copied = true
	}
	if !copied { // no changes: keep the text, bump the version
		text = old.Text
	}
	nd := &Doc{URI: old.URI, Path: old.Path, LanguageID: old.LanguageID,
		Version: p.TextDocument.Version, Text: text, starts: starts}
	s.docs[old.URI] = nd
	ch.New = nd
	return ch
}

// URIToPath converts a file: URI to a filesystem path; any other URI is
// returned unchanged as a string.
func URIToPath(uri lsp.DocumentURI) string {
	u, err := url.Parse(string(uri))
	if err != nil || u.Scheme != "file" {
		return string(uri)
	}
	return filepath.FromSlash(u.Path)
}

// PathToURI is the inverse of URIToPath for absolute filesystem paths.
func PathToURI(path string) lsp.DocumentURI {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return lsp.DocumentURI((&url.URL{Scheme: "file", Path: p}).String())
}
