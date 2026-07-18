// Command lspgen generates pkg/lsp's data-shape types from a pinned LSP
// metaModel.json (see metaModel.json in this directory, and config.go for the
// allowlist of structures/enumerations it draws from). This file holds the
// Go mirror of the metaModel.json schema itself (metaModel.schema.json
// upstream) — just enough of it to resolve the allowlisted names; kinds this
// generator doesn't need (tuple, literal, stringLiteral, booleanLiteral,
// integerLiteral, "and") are parsed structurally but rejected with a clear
// error if actually encountered, rather than silently mishandled.
package main

import "encoding/json"

// metaModel is the top-level shape of metaModel.json.
type metaModel struct {
	MetaData     metaData      `json:"metaData"`
	Structures   []structure   `json:"structures"`
	Enumerations []enumeration `json:"enumerations"`
	TypeAliases  []typeAlias   `json:"typeAliases"`
}

// metaData carries the metaModel's own version string (e.g. "3.18.0").
type metaData struct {
	Version string `json:"version"`
}

// structure is one metaModel.json "structures[]" entry: a named record type
// with optional single/multiple inheritance (extends/mixins) and its own
// properties.
type structure struct {
	Name       string     `json:"name"`
	Extends    []anyType  `json:"extends"`
	Mixins     []anyType  `json:"mixins"`
	Properties []property `json:"properties"`
}

// property is one field of a structure.
type property struct {
	Name       string  `json:"name"`
	Type       anyType `json:"type"`
	Optional   bool    `json:"optional"`
	Deprecated *string `json:"deprecated"`
}

// enumeration is one metaModel.json "enumerations[]" entry.
type enumeration struct {
	Name   string             `json:"name"`
	Type   anyType            `json:"type"` // kind:"base", name: "uinteger" | "string"
	Values []enumerationEntry `json:"values"`
}

// enumerationEntry is one named value of an enumeration. Value is a
// json.RawMessage because it is either a JSON number (int enums) or a JSON
// string (string enums) depending on the parent enumeration's Type.
type enumerationEntry struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

// typeAlias is one metaModel.json "typeAliases[]" entry: a named alias for
// another (often "or"-shaped) type. lspgen does not currently generate any
// typeAlias directly — every allowlisted alias-shaped concept (Definition,
// TextDocumentContentChangeEvent, MarkedString, ...) is either excluded
// (hand-written) or reached only via a structure property override — but the
// schema is parsed for completeness and possible future use.
type typeAlias struct {
	Name string  `json:"name"`
	Type anyType `json:"type"`
}

// anyType is the metaModel's polymorphic "Type" node: a discriminated union
// on Kind. Only the fields relevant to the kinds lspgen actually resolves
// (base, reference, array; extends/mixins entries are always "reference")
// are populated; other kinds (map, or, and, tuple, literal, stringLiteral,
// integerLiteral, booleanLiteral) parse structurally (kind + best-effort
// name) but are rejected by the resolver unless a field-level override
// intercepts them first — see resolveField in generate.go.
type anyType struct {
	Kind string `json:"kind"`

	// kind == "base" | "reference": the base-type or referenced-name.
	Name string `json:"name"`

	// kind == "array": the element type.
	Element *anyType `json:"element"`

	// kind == "map": key type (parsed for completeness; unsupported — no
	// allowlisted field resolves to a bare map, see config.go). Value is
	// json.RawMessage rather than *anyType because the SAME "value" JSON key
	// also carries a plain scalar (string/int/bool) for the
	// stringLiteral/integerLiteral/booleanLiteral kinds elsewhere in the
	// metaModel (outside the allowlist, but still parsed structurally since
	// the whole file is unmarshaled up front); this generator never needs to
	// interpret it either way, only detect+reject those kinds if reached.
	Key   *anyType        `json:"key"`
	Value json.RawMessage `json:"value"`

	// kind == "or" | "and" | "tuple": the member types (parsed for
	// completeness; every "or" occurrence in the allowlist must be resolved
	// via an explicit fieldOverride instead, see config.go).
	Items []anyType `json:"items"`
}

// byName indexes a metaModel's structures and enumerations by name for O(1)
// lookup during field resolution and extends/mixins flattening.
type byName struct {
	structs map[string]structure
	enums   map[string]enumeration
}

func newByName(mm *metaModel) byName {
	bn := byName{
		structs: make(map[string]structure, len(mm.Structures)),
		enums:   make(map[string]enumeration, len(mm.Enumerations)),
	}
	for _, s := range mm.Structures {
		bn.structs[s.Name] = s
	}
	for _, e := range mm.Enumerations {
		bn.enums[e.Name] = e
	}
	return bn
}
