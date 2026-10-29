package gotmpl

import (
	_ "embed"
	"sync"

	"github.com/davidwalter0/gotreesitter"
	"github.com/davidwalter0/gotreesitter/grammars"
)

// Name is the language name Register installs in the grammars registry.
const Name = "gotmpl"

//go:embed gotmpl.bin
var blob []byte

//go:embed highlights.scm
var highlightQuery string

var (
	langOnce sync.Once
	lang     *gotreesitter.Language
	langErr  error

	registerOnce sync.Once
)

// Language returns the compiled grammar, loading the embedded blob once.
func Language() (*gotreesitter.Language, error) {
	langOnce.Do(func() {
		lang, langErr = grammars.LoadLanguage(Name, blob)
	})
	return lang, langErr
}

// HighlightQuery returns the highlight query source registered with the
// grammar.
func HighlightQuery() string { return highlightQuery }

// Register adds the "gotmpl" grammar (extensions .tpl .gotmpl .tmpl) and its
// highlight query to the grammars registry. It is idempotent and safe for
// concurrent use.
//
// Note: when the process sets GOTREESITTER_GRAMMAR_SET, the registry silently
// drops names outside that allow-list; include "gotmpl" there.
func Register() {
	registerOnce.Do(func() {
		grammars.RegisterExtension(grammars.ExtensionEntry{
			Name:             Name,
			Extensions:       []string{".tpl", ".gotmpl", ".tmpl"},
			Aliases:          []string{"gotmpl", "go-template", "helm"},
			GenerateLanguage: Language,
			GrammarSource:    grammars.GrammarSourceGrammargenBlob,
			HighlightQuery:   highlightQuery,
		})
	})
}
