package broker

import (
	"strings"
	"testing"

	"github.com/davidwalter0/lspbridge/pkg/projectcontext"
)

// TestDefaultSpecForCoversResolvedLanguages pins the join that was missing
// when clangd and bash-language-server were first registered: a resolver in
// projectcontext.DefaultChain claims a file, and DefaultSpecFor must then
// know how to launch something for the language it claimed. A resolver with
// no matching case leaves a file "claimed then refused" — the caller gets
// "no language server configured for c" on a file the chain said it owned,
// which is a worse failure than never claiming it at all.
//
// The two must therefore move together, and this test is what makes that
// mechanical rather than remembered.
func TestDefaultSpecForCoversResolvedLanguages(t *testing.T) {
	langs := chainLanguages(t)
	if len(langs) == 0 {
		t.Fatal("no languages enumerated from DefaultChain — the enumeration broke, not the dispatch")
	}
	for _, lang := range langs {
		t.Run(lang, func(t *testing.T) {
			_, err := DefaultSpecFor(projectcontext.Context{Language: lang, Root: t.TempDir()})
			// A missing BINARY is an acceptable outcome — not every server is
			// installed on every host. An unconfigured LANGUAGE is the defect.
			if err != nil && strings.Contains(err.Error(), "no language server configured") {
				t.Fatalf("%q is resolved by DefaultChain but has no DefaultSpecFor case: %v", lang, err)
			}
		})
	}
}

// chainLanguages reports every languageId DefaultChain can stamp on a
// Context, including the per-extension variants LanguageFor produces (tsx ->
// typescriptreact), so the test above cannot silently stop covering a
// resolver somebody adds later.
func chainLanguages(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	var out []string
	add := func(l string) {
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	for _, r := range projectcontext.DefaultChain() {
		mr, ok := r.(projectcontext.MarkerResolver)
		if !ok {
			t.Fatalf("DefaultChain member %T is not a MarkerResolver; this enumeration needs updating", r)
		}
		add(mr.Language)
		for _, ext := range mr.Extensions {
			if mr.LanguageFor != nil {
				add(mr.LanguageFor(ext))
			}
		}
	}
	return out
}
