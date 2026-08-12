package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/broker"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// probeFixture is a tiny, deterministic Python source used to exercise a
// real documentSymbol -> references round trip. "greet" is called twice
// from "main" so references (with IncludeDeclaration) resolves to more than
// just the declaration, proving cross-reference resolution and not merely
// an echo of the query position.
const probeFixture = `def greet(name):
    return "hi " + name


def main():
    print(greet("a"))
    print(greet("b"))
`

// probeDir is a FIXED path, not a fresh temp directory per run: the broker
// keys its warm-session cache on (projectRoot, language) (see
// pkg/broker/broker.go), so a stable directory means a second doctor run
// within the idle-timeout window reuses the already-warm pyright session
// instead of cold-starting a new one and leaving an extra process for the
// idle reaper to clean up. The fixture file is rewritten on every run, so
// its content is always probeFixture regardless of what a previous run left
// there.
func probeDir() string {
	return filepath.Join(os.TempDir(), "lspdoctor-probe")
}

// probeEndToEnd exercises the actual capability, not just the socket: it
// resolves a real symbol via textDocument/documentSymbol, takes the
// position for the follow-up references call from THAT symbol's
// SelectionRange — never a hand-picked line/column — and confirms
// textDocument/references returns at least one location.
//
// This distinction is the point. A position that is not on an identifier
// returns a null references result that is INDISTINGUISHABLE, in the
// response alone, from "this symbol genuinely has no references". On
// 2026-07-25 a hand-picked probe position landed in the comment gap between
// two functions in a real file and the resulting null was recorded as "Dart
// is not wired" (mcp-ast todo 714a9133) — a false negative that stood for 18
// days and was cited as justification for a text-search fallback across a
// multi-day session. The same file, probed at a real identifier, returned
// 17 locations across 6 files. Deriving the probe position from
// documentSymbol's own SelectionRange, as this function does, makes that
// class of false negative structurally impossible here: the position is
// never guessed.
//
// A daemon that is up, reachable at the right path, and still answers null
// to every real query is a strictly worse failure than "not running" —
// every check above this one in the report would read PASS. That is
// exactly the failure this probe exists to catch.
//
// This probes Python/pyright as ONE representative language — the preset
// this repo's own broker-level live tests already exercise
// (broker_pyright_live_test.go), so it mirrors a proven pattern rather than
// inventing a new one. It stands for the tier generally, not a
// per-language matrix: the null-response ambiguity applies identically to
// every language the broker serves, not only the one that happened to be
// probed with a bad position in the recorded incident.
func probeEndToEnd(socketPath string, timeout time.Duration) result {
	const name = "end-to-end LSP probe (documentSymbol -> references, live pyright)"

	if _, err := exec.LookPath("pyright-langserver"); err != nil {
		return result{name: name, status: statusSkip,
			detail: "pyright-langserver not on PATH; cannot exercise a real session " +
				"(a toolchain/PATH gap, not evidence of a broker defect): " + err.Error()}
	}

	dir := probeDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return result{name: name, status: statusWarn, detail: "cannot create fixture dir: " + err.Error()}
	}
	pyPath := filepath.Join(dir, "probe.py")
	if err := os.WriteFile(pyPath, []byte(probeFixture), 0o644); err != nil {
		return result{name: name, status: statusWarn, detail: "cannot write fixture: " + err.Error()}
	}
	uri := lsp.DocumentURI("file://" + pyPath)
	text := probeFixture

	cli, err := broker.Dial(socketPath)
	if err != nil {
		return result{name: name, status: statusSkip,
			detail: "broker not reachable at " + socketPath + "; see the reachability checks above"}
	}
	defer func() { _ = cli.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	symParams, err := json.Marshal(lsp.DocumentSymbolParams{TextDocument: lsp.TextDocumentIdentifier{URI: uri}})
	if err != nil {
		return result{name: name, status: statusWarn, detail: "encode documentSymbol params: " + err.Error()}
	}
	sel, err := pollSelectionRange(ctx, cli, pyPath, &text, symParams, "greet")
	if err != nil {
		return result{name: name, status: statusFail,
			detail: "documentSymbol never resolved a 'greet' symbol through a live session: " + err.Error()}
	}

	refParams, err := json.Marshal(lsp.ReferenceParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     sel.Start,
		Context:      lsp.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		return result{name: name, status: statusWarn, detail: "encode references params: " + err.Error()}
	}
	raw, err := cli.Query(ctx, broker.QueryRequest{
		File: pyPath, Method: "textDocument/references", Params: refParams, Text: &text,
	})
	if err != nil {
		return result{name: name, status: statusFail, detail: "references query failed: " + err.Error()}
	}
	var locs []lsp.Location
	if unmarshalErr := json.Unmarshal(raw, &locs); unmarshalErr != nil || len(locs) == 0 {
		return result{name: name, status: statusFail, detail: fmt.Sprintf(
			"references at a position TAKEN FROM documentSymbol's SelectionRange returned %s — "+
				"the tier is reachable but not answering real queries. A null/empty result at a "+
				"HAND-PICKED position would not be usable evidence either way — see the doc comment "+
				"on probeEndToEnd and docs/design/broker-lifecycle-and-socket-path.org", string(raw))}
	}
	return result{name: name, status: statusPass,
		detail: fmt.Sprintf("%d reference location(s) resolved through a live pyright session at %s", len(locs), socketPath)}
}

// pollSelectionRange polls textDocument/documentSymbol until it finds a
// symbol named want, then returns its SelectionRange: the position a real
// consumer (mcp-ast's lsp-symbols tool) would hand to a follow-up
// references call. It stays at the raw broker.Client.Query level — the same
// level mcp-ast/ae actually call at (neither constructs a pkg/lsp.Client of
// its own; that type talks to a spawned server directly, bypassing the
// broker) — rather than a higher-level helper that would exercise a
// different path than production code.
func pollSelectionRange(ctx context.Context, cli *broker.Client, filePath string, text *string, params json.RawMessage, want string) (lsp.Range, error) {
	for {
		raw, err := cli.Query(ctx, broker.QueryRequest{
			File: filePath, Method: "textDocument/documentSymbol", Params: params, Text: text,
		})
		if err != nil {
			return lsp.Range{}, err
		}
		if rng, ok := findSelectionRange(raw, want); ok {
			return rng, nil
		}
		select {
		case <-ctx.Done():
			return lsp.Range{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// findSelectionRange looks for a symbol named want in raw — a
// textDocument/documentSymbol result in either its hierarchical
// (DocumentSymbol[]) or flat (SymbolInformation[]) shape — and returns its
// selection range.
//
// The two shapes are discriminated by inspecting the FIRST element for a
// "location" key, the same technique pkg/lsp.Client.DocumentSymbol uses (a
// SymbolInformation carries "location"; a DocumentSymbol carries
// "selectionRange" and no "location"). Unmarshaling straight into
// []DocumentSymbol and falling back to []SymbolInformation only when the
// result is empty is NOT equivalent and was the first draft of this
// function: encoding/json accepts missing fields silently, so a genuinely
// FLAT result would "succeed" as a slice of DocumentSymbol with every
// Range/SelectionRange left zero-valued, instead of falling through to the
// flat branch that has the real data.
func findSelectionRange(raw json.RawMessage, want string) (lsp.Range, bool) {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil || len(elems) == 0 {
		return lsp.Range{}, false
	}
	var probe struct {
		Location *json.RawMessage `json:"location"`
	}
	if err := json.Unmarshal(elems[0], &probe); err != nil {
		return lsp.Range{}, false
	}
	if probe.Location != nil {
		var flat []lsp.SymbolInformation
		if err := json.Unmarshal(raw, &flat); err != nil {
			return lsp.Range{}, false
		}
		for _, s := range flat {
			if s.Name == want {
				return s.Location.Range, true
			}
		}
		return lsp.Range{}, false
	}
	var hier []lsp.DocumentSymbol
	if err := json.Unmarshal(raw, &hier); err != nil {
		return lsp.Range{}, false
	}
	for _, s := range hier {
		if s.Name == want {
			return s.SelectionRange, true
		}
	}
	return lsp.Range{}, false
}
