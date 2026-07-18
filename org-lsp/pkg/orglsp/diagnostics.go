// This file is "org-lint-lite": the one and only diagnostics pass org-lsp
// runs. It intentionally checks exactly two things. See the README's
// Non-Goals for what it deliberately does not do (no babel, no agenda, no
// table formulas — those are BEHAVIOR checks that require a real Emacs).
package orglsp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/org/orgast"
)

// lintSource identifies org-lsp's diagnostics in the LSP Diagnostic.source
// field, letting a client (or a human reading its problems panel) tell them
// apart from another server's.
const lintSource = "org-lint-lite"

// Diagnostics runs org-lint-lite over text (parsed as docPath, whose
// directory resolves relative file: links): broken [[file:...]] links and
// unclosed #+begin_/#+end_ blocks. A nil/empty return means no problems
// found — [Server] still publishes that as an explicit empty batch so a
// previously-reported problem is cleared once fixed.
func Diagnostics(docPath, text string) []lsp.Diagnostic {
	var diags []lsp.Diagnostic
	doc := orgast.ParseString(docPath, text)
	lines := splitLines(text)

	diags = append(diags, brokenLinkDiagnostics(doc, docPath, lines)...)
	diags = append(diags, unclosedBlockDiagnostics(lines)...)
	return diags
}

// brokenLinkDiagnostics flags every [[file:TARGET]] link whose TARGET does
// not exist on disk, resolved relative to docPath's directory (absolute
// targets are checked as-is). A "::search-text" or "::N" suffix (org's
// within-file search/line addressing) is stripped before the existence
// check, since it addresses a location *inside* the target, not part of its
// path.
//
// Note the go-org quirk this works around: [orgast.Link.URL] is the RAW
// link text, "protocol:path" — go-org's parser never strips the protocol
// back off (see niklasfasching/go-org's parseRegularLink, which hands the
// unsplit string straight to ResolveLink). [orgast.Link.Protocol] carries
// the same protocol separately, so target must be computed by trimming
// "protocol:" off URL, not by using URL as the path directly.
func brokenLinkDiagnostics(doc *orgast.Document, docPath string, lines []string) []lsp.Diagnostic {
	links := orgast.Links(doc)
	if len(links) == 0 {
		return nil
	}
	linkLines := matchLinkLines(links, lines)
	dir := filepath.Dir(docPath)

	var diags []lsp.Diagnostic
	for i, l := range links {
		if l.Protocol != "file" || l.URL == "" {
			continue
		}
		target := strings.TrimPrefix(l.URL, l.Protocol+":")
		target = stripLinkSearchSuffix(target)
		resolved := target
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(dir, resolved)
		}
		if _, err := os.Stat(resolved); err != nil {
			diags = append(diags, lsp.Diagnostic{
				Range:    lineRange(linkLines[i], linkLines[i]+1, lines),
				Severity: lsp.SeverityWarning,
				Source:   lintSource,
				Message:  fmt.Sprintf("broken link: file target not found: %s", target),
			})
		}
	}
	return diags
}

func stripLinkSearchSuffix(target string) string {
	if idx := strings.Index(target, "::"); idx >= 0 {
		return target[:idx]
	}
	return target
}

// matchLinkLines aligns links (orgast's flat, in-order, crypt/PGP-filtered
// link list) against lines by the same in-order, never-reuse cursor
// strategy as [matchHeadingStarts], searching for each link's raw URL text.
func matchLinkLines(links []orgast.Link, lines []string) []int {
	out := make([]int, len(links))
	cursor := 0
	for i, l := range links {
		idx := findLineContaining(lines, cursor, l.URL)
		if idx < 0 {
			idx = cursor
			if idx >= len(lines) {
				idx = len(lines) - 1
			}
			if idx < 0 {
				idx = 0
			}
		}
		out[i] = idx
		cursor = idx + 1
	}
	return out
}

// unclosedBlockDiagnostics flags every #+begin_X with no matching #+end_X.
func unclosedBlockDiagnostics(lines []string) []lsp.Diagnostic {
	_, unclosed := scanBlocks(lines)
	if len(unclosed) == 0 {
		return nil
	}
	diags := make([]lsp.Diagnostic, 0, len(unclosed))
	for _, u := range unclosed {
		diags = append(diags, lsp.Diagnostic{
			Range:    lineRange(u.Line, u.Line+1, lines),
			Severity: lsp.SeverityError,
			Source:   lintSource,
			Message:  fmt.Sprintf("unclosed #+begin_%s block", strings.ToLower(u.Name)),
		})
	}
	return diags
}
