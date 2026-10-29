// Command gen regenerates gotmpl.bin from grammar.json. Run it through
// `go generate ./grammars/gotmpl` whenever grammar.json or the gotreesitter
// dependency changes: blobs are not portable across engine vintages.
//
// grammar.json is data (vendored from upstream, see README.md) and stays
// byte-identical to upstream; this program never evaluates grammar.js or any
// script from the upstream repository. Three accommodations for the
// gotreesitter generator are applied in memory before import, each measured
// against the engine at the pinned version:
//
//  1. Hidden rules are listed in Grammar.PreserveHiddenChoicePassthrough.
//     Otherwise grammargen flattens hidden choice rules such as
//     _left_delimiter and _right_delimiter into every parent, multiplying
//     range_action alone into ~25,000 productions (38,901 in total); the LR
//     build then does not finish in ten minutes. With it: 186 productions,
//     about nine seconds. Hidden rules produce no tree nodes either way.
//  2. _right_delimiter's members are token("}}") and token("-}}"). As
//     written, the engine leaves those tokens out of the tree; as plain
//     strings (the same language) they appear as "}}" / "-}}" nodes, which
//     the highlight query and YAMLRanges rely on.
//  3. comment is an immediate token upstream. After the whitespace-consuming
//     "{{- " delimiter the engine rejects an immediate token, so
//     `{{- /* c */ -}}` (common in helm _helpers.tpl) parses as ERROR. As a
//     plain token it parses; the only change in accepted input is that
//     `{{ /* c */ }}`, which Go itself rejects, gets its comment recognised
//     and then a MISSING "-}}" (still flagged as an error).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/davidwalter0/gotreesitter/grammargen"
)

func main() {
	in := flag.String("json", "grammar.json", "resolved tree-sitter grammar.json")
	out := flag.String("bin", "gotmpl.bin", "output language blob")
	flag.Parse()
	if err := run(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run(in, out string) error {
	data, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	g, err := grammargen.ImportGrammarJSON(data)
	if err != nil {
		return fmt.Errorf("import %s: %w", in, err)
	}
	if err := accommodate(g); err != nil {
		return err
	}
	for _, name := range g.RuleOrder {
		if strings.HasPrefix(name, "_") {
			g.PreserveHiddenChoicePassthrough = append(g.PreserveHiddenChoicePassthrough, name)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	_, blob, err := grammargen.GenerateLanguageAndBlobWithContext(ctx, g)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}
	return os.WriteFile(out, blob, 0o644)
}

// accommodate applies accommodations 2 and 3 above to the imported grammar.
// It edits the IR, not the JSON: the first rule of grammar.json is the start
// rule, and a JSON object round trip would not preserve that order.
func accommodate(g *grammargen.Grammar) error {
	comment := g.Rules["comment"]
	if comment == nil || comment.Kind != grammargen.RuleImmToken {
		return fmt.Errorf("rule comment is not an immediate token; upstream changed, re-measure")
	}
	comment.Kind = grammargen.RuleToken

	rd := g.Rules["_right_delimiter"]
	if rd == nil || rd.Kind != grammargen.RuleChoice || len(rd.Children) == 0 {
		return fmt.Errorf("rule _right_delimiter is not a choice; upstream changed, re-measure")
	}
	for i, m := range rd.Children {
		if m.Kind != grammargen.RuleToken || len(m.Children) != 1 || m.Children[0].Kind != grammargen.RuleString {
			return fmt.Errorf("_right_delimiter member %d is not token(string); upstream changed, re-measure", i)
		}
		rd.Children[i] = m.Children[0]
	}
	return nil
}
