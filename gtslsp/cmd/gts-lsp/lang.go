package main

import "github.com/davidwalter0/lspbridge/gtslsp/gts"

// registerExtraLanguages registers grammars that gotreesitter does not ship.
//
// TODO(merge, gotmpl): connect the gotmpl package here:
//
//	gotmpl.Register() // registers language "gotmpl"
//
// (import "github.com/davidwalter0/lspbridge/gtslsp/grammars/gotmpl").
func registerExtraLanguages() {}

// hooks returns the language-detection and parse-view hooks.
//
// TODO(merge, gotmpl): once gotmpl lands, return
//
//	gts.Hooks{
//	    DetectLanguage: func(path string, _ []byte) string {
//	        if gotmpl.IsHelmTemplate(path) { return "gotmpl" }
//	        return ""
//	    },
//	    ParseView: func(_, lang string, src []byte) []byte {
//	        if lang == "yaml" { return gotmpl.BlankActions(src) }
//	        return src
//	    },
//	}
//
// The exact split between the two (which Helm files parse as gotmpl and which
// as YAML-with-blanked-actions) is the gotmpl package's contract; connect it
// at merge.
func hooks() gts.Hooks { return gts.Hooks{} }
