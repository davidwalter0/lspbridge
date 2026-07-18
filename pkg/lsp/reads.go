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

// Read/rename method names.
const (
	documentSymbolMethod = "textDocument/documentSymbol"
	referencesMethod     = "textDocument/references"
	definitionMethod     = "textDocument/definition"
	hoverMethod          = "textDocument/hover"
	renameMethod         = "textDocument/rename"
	foldingRangeMethod   = "textDocument/foldingRange"
)

// TextDocumentIdentifier names a document by URI.
type TextDocumentIdentifier struct {
	URI DocumentURI `json:"uri"`
}

// SymbolKind is the LSP symbol-kind enumeration (1..26).
type SymbolKind int

// LSP symbol kinds.
const (
	SymbolKindFile          SymbolKind = 1
	SymbolKindModule        SymbolKind = 2
	SymbolKindNamespace     SymbolKind = 3
	SymbolKindPackage       SymbolKind = 4
	SymbolKindClass         SymbolKind = 5
	SymbolKindMethod        SymbolKind = 6
	SymbolKindProperty      SymbolKind = 7
	SymbolKindField         SymbolKind = 8
	SymbolKindConstructor   SymbolKind = 9
	SymbolKindEnum          SymbolKind = 10
	SymbolKindInterface     SymbolKind = 11
	SymbolKindFunction      SymbolKind = 12
	SymbolKindVariable      SymbolKind = 13
	SymbolKindConstant      SymbolKind = 14
	SymbolKindString        SymbolKind = 15
	SymbolKindNumber        SymbolKind = 16
	SymbolKindBoolean       SymbolKind = 17
	SymbolKindArray         SymbolKind = 18
	SymbolKindObject        SymbolKind = 19
	SymbolKindKey           SymbolKind = 20
	SymbolKindNull          SymbolKind = 21
	SymbolKindEnumMember    SymbolKind = 22
	SymbolKindStruct        SymbolKind = 23
	SymbolKindEvent         SymbolKind = 24
	SymbolKindOperator      SymbolKind = 25
	SymbolKindTypeParameter SymbolKind = 26
)

// SymbolTag is the LSP symbol-tag enumeration.
type SymbolTag int

// SymbolTagDeprecated marks a symbol as deprecated.
const SymbolTagDeprecated SymbolTag = 1

// DocumentSymbol is the modern, hierarchical result element of
// textDocument/documentSymbol: a symbol with a nesting Range, a narrower
// SelectionRange (the name), and nested Children. A server returns this shape
// only when the client advertises hierarchicalDocumentSymbolSupport; otherwise
// it returns the flat [SymbolInformation] form.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           SymbolKind       `json:"kind"`
	Tags           []SymbolTag      `json:"tags,omitempty"`
	Deprecated     bool             `json:"deprecated,omitempty"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// SymbolInformation is the legacy, flat result element of
// textDocument/documentSymbol (and the workspace/symbol result): a symbol with
// a full [Location] rather than nested ranges. ContainerName is the enclosing
// symbol's name, if any.
type SymbolInformation struct {
	Name          string      `json:"name"`
	Kind          SymbolKind  `json:"kind"`
	Tags          []SymbolTag `json:"tags,omitempty"`
	Deprecated    bool        `json:"deprecated,omitempty"`
	Location      Location    `json:"location"`
	ContainerName string      `json:"containerName,omitempty"`
}

// DocumentSymbolParams is the textDocument/documentSymbol request payload.
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

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

// ReferenceContext scopes a references request; IncludeDeclaration adds the
// symbol's own declaration to the returned locations.
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// ReferenceParams is the textDocument/references request payload. Position is
// 0-indexed with a UTF-16 character offset.
type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      ReferenceContext       `json:"context"`
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

// DefinitionParams is the textDocument/definition request payload. Position is
// 0-indexed with a UTF-16 character offset.
type DefinitionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
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

// HoverParams is the textDocument/hover request payload. Position is 0-indexed
// with a UTF-16 character offset.
type HoverParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// MarkupContent is rendered hover/documentation content. Kind is "plaintext"
// or "markdown".
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Hover is the textDocument/hover result: rendered Contents and an optional
// Range the hover applies to. The LSP Contents field is polymorphic
// (MarkedString | MarkedString[] | MarkupContent); [Hover.UnmarshalJSON]
// normalizes every form to a single [MarkupContent].
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

// RenameParams is the textDocument/rename request payload: rename the symbol at
// Position (0-indexed, UTF-16) to NewName. The server computes the edit purely
// and returns a [WorkspaceEdit]; it never writes to disk.
type RenameParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	NewName      string                 `json:"newName"`
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

// FoldingRangeParams is the textDocument/foldingRange request payload.
type FoldingRangeParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// FoldingRangeKind categorizes a [FoldingRange] (e.g. so a client's "fold all
// comments" command can select just that kind). Per the LSP spec it is an
// open string enumeration — [FoldingRangeKindComment], [FoldingRangeKindImports]
// and [FoldingRangeKindRegion] are the standardized values (LSP 3.18
// metaModel, FoldingRangeKind), but a server may report others.
type FoldingRangeKind string

// Standardized folding range kinds.
const (
	FoldingRangeKindComment FoldingRangeKind = "comment"
	FoldingRangeKindImports FoldingRangeKind = "imports"
	FoldingRangeKindRegion  FoldingRangeKind = "region"
)

// FoldingRange is one foldable line span. StartCharacter/EndCharacter are
// pointers because the LSP spec gives the unset case its own meaning
// ("defaults to the length of the start/end line") distinct from character 0;
// a server that folds whole lines (as org-lsp does) leaves both nil.
type FoldingRange struct {
	StartLine      int              `json:"startLine"`
	StartCharacter *int             `json:"startCharacter,omitempty"`
	EndLine        int              `json:"endLine"`
	EndCharacter   *int             `json:"endCharacter,omitempty"`
	Kind           FoldingRangeKind `json:"kind,omitempty"`
	CollapsedText  string           `json:"collapsedText,omitempty"`
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
