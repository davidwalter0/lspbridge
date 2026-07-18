// Command lspgen generates pkg/lsp/types_gen.go from a pinned copy of the LSP
// metaModel.json vendored at cmd/lspgen/metaModel.json (embedded into this
// binary via go:embed, so it works regardless of the invoking CWD). See
// config.go for the allowlist of structures/enumerations it generates from,
// and doc.go / go:generate wiring in pkg/lsp.
//
// mgmt 8a6c4d87: lspbridge's pkg/lsp types were hand-written while the
// method surface was small (~6 methods/10 types); this generator exists so
// the type surface can grow with fidelity to spec instead of by
// transcription, while a deliberately small set of hand-tuned types
// (WorkspaceEdit, Hover, ...) stay hand-written — see config.go's exclusion
// list for the full rationale on each.
package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
)

//go:embed metaModel.json
var metaModelJSON []byte

// metaModelSHA256 pins the exact vendored metaModel.json this generator
// trusts. Fetched 2026-07-18 from
// https://raw.githubusercontent.com/microsoft/language-server-protocol/gh-pages/_specifications/lsp/3.18/metaModel/metaModel.json
// (LSP 3.18.0). Verifying this at every run means a corrupted or
// accidentally-edited vendor copy fails loudly instead of silently
// generating from the wrong spec text.
const metaModelSHA256 = "caae8df639a4248520a3f589fd72945365e9d8ebca5baf564161a515430d9d41"

func main() {
	out := flag.String("out", "", "output file path (required)")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "lspgen: -out is required")
		os.Exit(1)
	}

	sum := sha256.Sum256(metaModelJSON)
	if got := hex.EncodeToString(sum[:]); got != metaModelSHA256 {
		fmt.Fprintf(os.Stderr,
			"lspgen: embedded metaModel.json sha256 mismatch: got %s, want %s\n"+
				"(the vendored copy changed without updating metaModelSHA256 in main.go — re-verify before bumping the pin)\n",
			got, metaModelSHA256)
		os.Exit(1)
	}

	generated, err := Generate(metaModelJSON)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lspgen: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(*out, generated, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "lspgen: write %s: %v\n", *out, err)
		os.Exit(1)
	}
}
