package main

import (
	"github.com/davidwalter0/lspbridge/gtslsp/grammars/gotmpl"
	"github.com/davidwalter0/lspbridge/gtslsp/gts"
)

// registerExtraLanguages registers grammars that gotreesitter does not ship:
// gotmpl (Go text/template, helm's .tpl helpers).
func registerExtraLanguages() { gotmpl.Register() }

// hooks returns the language-detection and parse-view hooks.
//
// A helm chart template (templates/*.yaml) stays a YAML document: it is
// parsed with every {{ ... }} action blanked to spaces of equal length, so
// YAML symbols, folding and syntax errors line up with the real text while
// template-only constructs never read as YAML errors. The template layer
// itself is checked by the helm validator on save; .tpl helpers detect as
// gotmpl by extension.
func hooks() gts.Hooks {
	return gts.Hooks{
		ParseView: func(path, lang string, src []byte) []byte {
			if lang == "yaml" && gotmpl.IsHelmTemplate(path) {
				return gotmpl.BlankActions(src)
			}
			return src
		},
	}
}
