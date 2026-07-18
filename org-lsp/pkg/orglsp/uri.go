package orglsp

import (
	"path/filepath"
	"strings"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// pathToURI renders an absolute filesystem path as a "file://" DocumentURI.
// It does not percent-encode special characters — an accepted v1
// simplification for a server whose own workspace scan produces every URI it
// hands back (org-lsp never needs to round-trip a URI a client authored with
// unusual characters); see the README's Non-Goals.
func pathToURI(path string) lsp.DocumentURI {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return lsp.DocumentURI("file://" + filepath.ToSlash(abs))
}

// uriToPath is the inverse of [pathToURI]: it strips a "file://" prefix,
// leaving the absolute path. Anything not of that form is returned as-is
// (best effort) rather than erroring, since callers use the result only for
// diagnostics/log messages and relative-link resolution.
func uriToPath(uri lsp.DocumentURI) string {
	return strings.TrimPrefix(string(uri), "file://")
}
