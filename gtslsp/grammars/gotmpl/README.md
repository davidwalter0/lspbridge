# gotmpl: Go text/template grammar for helm charts

Registers the `gotmpl` language (extensions `.tpl`, `.gotmpl`, `.tmpl`) with
gotreesitter's registry, out of tree. No fork of gotreesitter is involved.

## Provenance

| item | value |
|---|---|
| upstream | https://github.com/ngalaiko/tree-sitter-go-template |
| commit | `06b2fd2ac32a7be71cdcf925eeeb868a68e2acde` (default branch HEAD, committed 2026-10-02) |
| fetched | 2026-10-29 |
| licence | MIT, Copyright (c) 2021 Nikita Galaiko (`LICENSE.tree-sitter-go-template`) |
| `grammar.json` | upstream `src/grammar.json`, byte-identical, sha256 `e98437c357ed93894c683f51e75924fdd9d231af1774b8f08fb9b57f6980c7d6` |
| `highlights.scm` | upstream `queries/highlights.scm`, byte-identical |

Only the resolved `grammar.json`, the highlight query and the licence were
taken. `grammar.js` was never evaluated and no upstream script was run.

The upstream "helm" dialect (`dialects/helm`) is the same parser with
different queries (injections); it contains no grammar of its own. The
grammar has no `externals` and upstream ships no `scanner.c`, so no Go
scanner port is needed.

## Regenerating

    go generate ./grammars/gotmpl      # grammar.json -> gotmpl.bin (about 7 s)

`gotmpl.bin` must be regenerated whenever `grammar.json` or the
`github.com/davidwalter0/gotreesitter` version in `gtslsp/go.mod` changes;
`TestBlobMatchesGenerator` fails on a stale blob. The generator applies three
documented in-memory accommodations to the gotreesitter generator (see
`internal/gen/main.go`); without the first, generation does not finish in ten
minutes.

## API

    func Register()
    func IsHelmTemplate(path string) bool
    func BlankActions(src []byte) []byte
    func YAMLRanges(tree *gotreesitter.Tree, src []byte) []gotreesitter.Range

Also exported: `Name`, `Language()`, `HighlightQuery()`.

Registry note: when the process sets `GOTREESITTER_GRAMMAR_SET`, the registry
drops names outside that allow-list; include `gotmpl`.

## Known behaviour

- A helm `{{ include "x" . | nindent 4 }}` parses as a `function_call`
  whose `argument_list` holds a `chained_pipeline`: upstream's precedence, not
  an engine artefact.
- A missing `{{ end }}` is reported at the line of the unclosed `if`/`range`/
  `with`/`define`, not at end of file.
