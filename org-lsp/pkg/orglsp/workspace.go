package orglsp

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/org/orgast"
)

// indexedHeading is the small, cached projection of one heading a
// [WorkspaceIndex] needs to answer workspace/symbol: a title to match the
// query against, a line to build a Location from, and a Kind.
type indexedHeading struct {
	title string
	line  int
	kind  lsp.SymbolKind
}

// fileIndex is one *.org file's cached heading index, keyed by mtime — see
// [WorkspaceIndex.indexFile].
type fileIndex struct {
	modTime  int64 // Unix nanoseconds
	headings []indexedHeading
}

// WorkspaceIndex answers workspace/symbol by lazily scanning an org root
// directory for *.org files and matching a query against every non-crypt
// heading title. Each file's heading index is cached by mtime, so a repeat
// query over an unchanged tree does no parsing beyond a Stat per file.
// Zero value is not usable; construct with [NewWorkspaceIndex].
type WorkspaceIndex struct {
	root string

	mu    sync.Mutex
	cache map[string]fileIndex // absolute path -> cached index
}

// NewWorkspaceIndex returns an index scoped to root (the initialize
// request's rootUri/rootPath, as a filesystem path).
func NewWorkspaceIndex(root string) *WorkspaceIndex {
	return &WorkspaceIndex{root: root, cache: make(map[string]fileIndex)}
}

// Symbols answers workspace/symbol for query: a case-insensitive substring
// match (the todo's explicit instruction; the LSP spec's own suggested
// "characters appear in order" fuzzy match is a looser superset, so a
// substring match is a spec-compatible, if stricter, choice) against every
// heading title in every *.org file under the root. An empty query matches
// every heading. A file that fails to read or parse is skipped rather than
// failing the whole query.
func (w *WorkspaceIndex) Symbols(query string) ([]lsp.SymbolInformation, error) {
	files, err := w.orgFiles()
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)

	var out []lsp.SymbolInformation
	for _, path := range files {
		idx, err := w.indexFile(path)
		if err != nil {
			continue
		}
		uri := pathToURI(path)
		for _, h := range idx.headings {
			if q != "" && !strings.Contains(strings.ToLower(h.title), q) {
				continue
			}
			out = append(out, lsp.SymbolInformation{
				Name: h.title,
				Kind: h.kind,
				Location: lsp.Location{
					URI: uri,
					Range: lsp.Range{
						Start: lsp.Position{Line: h.line, Character: 0},
						End:   lsp.Position{Line: h.line, Character: 0},
					},
				},
			})
		}
	}
	return out, nil
}

// indexFile returns path's cached heading index, re-parsing only if the
// file's mtime has changed since it was last indexed.
func (w *WorkspaceIndex) indexFile(path string) (fileIndex, error) {
	st, err := os.Stat(path)
	if err != nil {
		return fileIndex{}, err
	}
	mtime := st.ModTime().UnixNano()

	w.mu.Lock()
	cached, ok := w.cache[path]
	w.mu.Unlock()
	if ok && cached.modTime == mtime {
		return cached, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fileIndex{}, err
	}
	text := string(data)
	doc := orgast.ParseString(path, text)
	headings := orgast.Headings(doc)
	lines := splitLines(text)
	headingLines := scanHeadingLines(lines)
	starts := matchHeadingStarts(headings, headingLines)

	idx := fileIndex{modTime: mtime}
	for i, h := range headings {
		idx.headings = append(idx.headings, indexedHeading{
			title: headingName(h),
			line:  starts[i],
			kind:  symbolKindForHeading(hasChildHeading(headings, i)),
		})
	}

	w.mu.Lock()
	w.cache[path] = idx
	w.mu.Unlock()
	return idx, nil
}

// orgFiles walks root for *.org files, skipping hidden directories (names
// starting with "." — .git, .worktree, etc.), matching case-insensitively.
func (w *WorkspaceIndex) orgFiles() ([]string, error) {
	var files []string
	err := filepath.WalkDir(w.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip it, don't fail the whole walk
		}
		if d.IsDir() {
			if p != w.root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".org") {
			files = append(files, p)
		}
		return nil
	})
	return files, err
}
