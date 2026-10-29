package gts

import (
	"embed"
	"path"
)

// overrideTags holds the per-language tags queries shipped with gts-lsp
// (queries/<language>/tags.scm). A language with an override uses it INSTEAD
// of gotreesitter's resolved (explicit or inferred) tags query: the inferred
// queries are empty for data/markup languages and, for C and C++, report type
// uses as definitions.
//
//go:embed queries/*/tags.scm
var overrideTags embed.FS

func overrideTagsQuery(lang string) (string, bool) {
	b, err := overrideTags.ReadFile(path.Join("queries", lang, "tags.scm"))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// OverrideLanguages lists the languages that carry a shipped tags override.
func OverrideLanguages() []string {
	ents, _ := overrideTags.ReadDir("queries")
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}
