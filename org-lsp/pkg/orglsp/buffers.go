package orglsp

import "sync"

// bufferState is one open document's in-memory text and version.
type bufferState struct {
	text    string
	version int
}

// Buffers is the didOpen/didChange open-document map: org-lsp's only
// mutable per-document state. Sync is whole-document only
// (textDocumentSync=Full, see [Server]): Change always replaces Text
// wholesale, matching the single [lsp.TextDocumentContentChangeEvent] a
// Full-sync client sends (it carries no incremental Range). Safe for
// concurrent use.
type Buffers struct {
	mu   sync.RWMutex
	docs map[string]bufferState // key: URI string
}

// NewBuffers returns an empty buffer map.
func NewBuffers() *Buffers {
	return &Buffers{docs: make(map[string]bufferState)}
}

// Open records a newly opened document.
func (b *Buffers) Open(uri, text string, version int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.docs[uri] = bufferState{text: text, version: version}
}

// Change replaces an open document's text (whole-document sync).
func (b *Buffers) Change(uri, text string, version int) {
	b.Open(uri, text, version)
}

// Close forgets a document (textDocument/didClose).
func (b *Buffers) Close(uri string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.docs, uri)
}

// Get returns uri's current text and whether it is open.
func (b *Buffers) Get(uri string) (string, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	s, ok := b.docs[uri]
	return s.text, ok
}
