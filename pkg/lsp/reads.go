package lsp

import (
	"context"
	"encoding/json"
	"strings"
)

// This file adds the READ surface the broker multiplexes to mcp-ast:
// documentSymbol, references, definition, hover, foldingRange — plus rename,
// the first LSP method that produces a [WorkspaceEdit] and so feeds the
// wsedit WRITE path.
//
// All positions are LSP positions: 0-indexed lines, and characters measured in
// UTF-16 code units (see [Position]). Callers that speak the structast DTO
// (1-indexed, UTF-8) convert at the boundary; the write path converts via
// [github.com/davidwalter0/lspbridge/pkg/wsedit.ByteOffset].
//
// The request Params/result-element types this file's methods use
// (TextDocumentIdentifier, SymbolKind, SymbolTag, DocumentSymbol,
// SymbolInformation, DocumentSymbolParams, ReferenceContext, ReferenceParams,
// DefinitionParams, HoverParams, MarkupContent, RenameParams,
// FoldingRangeParams, FoldingRangeKind, FoldingRange) are generated —
// see types_gen.go and cmd/lspgen/config.go. DocumentSymbolResult and Hover
// stay hand-written here: DocumentSymbolResult has no metaModel structure of
// its own (it's this package's own discriminator over the
// DocumentSymbol[] | SymbolInformation[] | null result), and Hover's
// [Hover.UnmarshalJSON] normalizes a polymorphic wire shape that is itself
// out of scope for generation (see config.go).

// Read/rename method names.
const (
	documentSymbolMethod = "textDocument/documentSymbol"
	referencesMethod     = "textDocument/references"
	definitionMethod     = "textDocument/definition"
	hoverMethod          = "textDocument/hover"
	renameMethod         = "textDocument/rename"
	foldingRangeMethod   = "textDocument/foldingRange"
)

// DocumentSymbolResult carries whichever of the two shapes the server returned.
// textDocument/documentSymbol replies with EITHER a hierarchical
// DocumentSymbol[] OR a flat SymbolInformation[] (never both), so exactly one
// slice is populated; both are nil for an empty/absent result. Hierarchical is
// preferred and returned when the server supports it.
type DocumentSymbolResult struct {
	Hierarchical []DocumentSymbol
	Flat         []SymbolInformation
}

// DocumentSymbol requests the symbol tree (or flat list) for a document. The
// two possible result shapes are discriminated by inspecting the first element:
// a SymbolInformation carries a "location" field, a DocumentSymbol carries a
// "selectionRange" and none. A null or empty result yields an empty
// [DocumentSymbolResult] and a nil error.
func (c *Client) DocumentSymbol(ctx context.Context, params DocumentSymbolParams) (*DocumentSymbolResult, error) {
	var raw json.RawMessage
	if err := c.conn.Call(ctx, documentSymbolMethod, params, &raw); err != nil {
		return nil, err
	}
	res := &DocumentSymbolResult{}
	if isJSONNull(raw) {
		return res, nil
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, err
	}
	if len(elems) == 0 {
		return res, nil
	}
	// Discriminate on the first element: SymbolInformation has "location".
	var probe struct {
		Location *json.RawMessage `json:"location"`
	}
	if err := json.Unmarshal(elems[0], &probe); err != nil {
		return nil, err
	}
	if probe.Location != nil {
		if err := json.Unmarshal(raw, &res.Flat); err != nil {
			return nil, err
		}
		return res, nil
	}
	if err := json.Unmarshal(raw, &res.Hierarchical); err != nil {
		return nil, err
	}
	return res, nil
}

// References returns every location that references the symbol at the given
// position. A null result yields a nil slice and a nil error.
func (c *Client) References(ctx context.Context, params ReferenceParams) ([]Location, error) {
	var raw json.RawMessage
	if err := c.conn.Call(ctx, referencesMethod, params, &raw); err != nil {
		return nil, err
	}
	if isJSONNull(raw) {
		return nil, nil
	}
	var locs []Location
	if err := json.Unmarshal(raw, &locs); err != nil {
		return nil, err
	}
	return locs, nil
}

// Definition returns the definition location(s) of the symbol at the given
// position. The LSP result is Location | Location[] | null; all three are
// normalized to a slice (nil for null). Because this client does not advertise
// linkSupport, servers reply with Location(s), never LocationLink[].
func (c *Client) Definition(ctx context.Context, params DefinitionParams) ([]Location, error) {
	var raw json.RawMessage
	if err := c.conn.Call(ctx, definitionMethod, params, &raw); err != nil {
		return nil, err
	}
	return decodeLocations(raw)
}

// decodeLocations normalizes an LSP Location | Location[] | null payload into a
// slice. It discriminates on the first significant byte so a single object and
// an array are both accepted.
func decodeLocations(raw json.RawMessage) ([]Location, error) {
	if isJSONNull(raw) {
		return nil, nil
	}
	switch firstToken(raw) {
	case '[':
		var locs []Location
		if err := json.Unmarshal(raw, &locs); err != nil {
			return nil, err
		}
		return locs, nil
	case '{':
		var loc Location
		if err := json.Unmarshal(raw, &loc); err != nil {
			return nil, err
		}
		return []Location{loc}, nil
	default:
		return nil, nil
	}
}

// Hover is the textDocument/hover result: rendered Contents and an optional
// Range the hover applies to. The LSP Contents field is polymorphic
// (MarkedString | MarkedString[] | MarkupContent); [Hover.UnmarshalJSON]
// normalizes every form to a single [MarkupContent].
//
// Hover is hand-written rather than generated (cmd/lspgen/config.go): this
// normalization is behavior, not a mechanical data shape, and MarkedString is
// not modeled at all.
type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

// UnmarshalJSON normalizes the polymorphic Contents field. A bare string and a
// MarkedString object ({language, value}) become plaintext; a MarkupContent
// object ({kind, value}) is taken as-is; an array is joined value-by-value with
// blank lines, its kind taken from the first markup element (else plaintext).
func (h *Hover) UnmarshalJSON(data []byte) error {
	var shim struct {
		Contents json.RawMessage `json:"contents"`
		Range    *Range          `json:"range"`
	}
	if err := json.Unmarshal(data, &shim); err != nil {
		return err
	}
	h.Range = shim.Range
	mc, err := decodeMarkup(shim.Contents)
	if err != nil {
		return err
	}
	h.Contents = mc
	return nil
}

// decodeMarkup collapses one Contents value (string | MarkedString |
// MarkupContent | an array of those) into a single MarkupContent.
func decodeMarkup(raw json.RawMessage) (MarkupContent, error) {
	if isJSONNull(raw) || len(raw) == 0 {
		return MarkupContent{Kind: "plaintext"}, nil
	}
	switch firstToken(raw) {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return MarkupContent{}, err
		}
		return MarkupContent{Kind: "plaintext", Value: s}, nil
	case '{':
		return decodeMarkupObject(raw)
	case '[':
		var elems []json.RawMessage
		if err := json.Unmarshal(raw, &elems); err != nil {
			return MarkupContent{}, err
		}
		parts := make([]string, 0, len(elems))
		kind := "plaintext"
		for i, e := range elems {
			mc, err := decodeMarkup(e)
			if err != nil {
				return MarkupContent{}, err
			}
			if i == 0 && mc.Kind == "markdown" {
				kind = "markdown"
			}
			parts = append(parts, mc.Value)
		}
		return MarkupContent{Kind: kind, Value: strings.Join(parts, "\n\n")}, nil
	default:
		return MarkupContent{Kind: "plaintext"}, nil
	}
}

// decodeMarkupObject decodes a Contents object that is either a MarkupContent
// ({kind, value}) or a MarkedString ({language, value}). A MarkedString is
// rendered as markdown to preserve its fenced-code intent.
func decodeMarkupObject(raw json.RawMessage) (MarkupContent, error) {
	var obj struct {
		Kind     string `json:"kind"`
		Value    string `json:"value"`
		Language string `json:"language"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return MarkupContent{}, err
	}
	if obj.Kind != "" {
		return MarkupContent{Kind: obj.Kind, Value: obj.Value}, nil
	}
	// MarkedString {language, value}: treat as markdown.
	return MarkupContent{Kind: "markdown", Value: obj.Value}, nil
}

// Hover requests hover information at a position. A null result (no hover
// available) yields (nil, nil).
func (c *Client) Hover(ctx context.Context, params HoverParams) (*Hover, error) {
	var raw json.RawMessage
	if err := c.conn.Call(ctx, hoverMethod, params, &raw); err != nil {
		return nil, err
	}
	if isJSONNull(raw) {
		return nil, nil
	}
	var h Hover
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// Rename requests the [WorkspaceEdit] that renames the symbol at Position to
// NewName. A null result (rename not possible at that position) yields
// (nil, nil). The returned edit is normalized by [WorkspaceEdit.UnmarshalJSON]
// so BOTH wire forms — "changes" and "documentChanges" (pyright uses the
// latter) — surface through the single Changes map that feeds the write path,
// [github.com/davidwalter0/lspbridge/pkg/wsedit.FromWorkspaceEdit].
func (c *Client) Rename(ctx context.Context, params RenameParams) (*WorkspaceEdit, error) {
	var raw json.RawMessage
	if err := c.conn.Call(ctx, renameMethod, params, &raw); err != nil {
		return nil, err
	}
	if isJSONNull(raw) {
		return nil, nil
	}
	var we WorkspaceEdit
	if err := json.Unmarshal(raw, &we); err != nil {
		return nil, err
	}
	return &we, nil
}

// FoldingRange requests the foldable line ranges for a document. A null
// result yields a nil slice and a nil error.
func (c *Client) FoldingRange(ctx context.Context, params FoldingRangeParams) ([]FoldingRange, error) {
	var raw json.RawMessage
	if err := c.conn.Call(ctx, foldingRangeMethod, params, &raw); err != nil {
		return nil, err
	}
	if isJSONNull(raw) {
		return nil, nil
	}
	var ranges []FoldingRange
	if err := json.Unmarshal(raw, &ranges); err != nil {
		return nil, err
	}
	return ranges, nil
}

// isJSONNull reports whether raw is absent or the JSON null literal.
func isJSONNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(bytesTrimSpace(raw)) == "null"
}

// firstToken returns the first non-whitespace byte of raw, or 0 if none.
func firstToken(raw json.RawMessage) byte {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return b
		}
	}
	return 0
}

// bytesTrimSpace trims ASCII JSON whitespace from both ends of raw without
// pulling in bytes.TrimSpace's Unicode handling (JSON whitespace is ASCII).
func bytesTrimSpace(raw json.RawMessage) json.RawMessage {
	i, j := 0, len(raw)
	for i < j {
		switch raw[i] {
		case ' ', '\t', '\r', '\n':
			i++
			continue
		}
		break
	}
	for j > i {
		switch raw[j-1] {
		case ' ', '\t', '\r', '\n':
			j--
			continue
		}
		break
	}
	return raw[i:j]
}
