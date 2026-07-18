package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"strconv"
	"strings"
	"text/template"
	"unicode"
)

// commonInitialisms mirrors golint's well-known initialisms list (ACL, API,
// ..., XSS): every metaModel property name this generator processes is
// simple lowerCamelCase (no acronym runs), so the only ones that actually
// fire are "id" and "uri" — but the full list costs nothing and keeps
// exportedName correct if a future allowlist addition needs another.
var commonInitialisms = map[string]bool{
	"ACL": true, "API": true, "ASCII": true, "CPU": true, "CSS": true,
	"DNS": true, "EOF": true, "GUID": true, "HTML": true, "HTTP": true,
	"HTTPS": true, "ID": true, "IP": true, "JSON": true, "LHS": true,
	"QPS": true, "RAM": true, "RHS": true, "RPC": true, "SLA": true,
	"SMTP": true, "SQL": true, "SSH": true, "TCP": true, "TLS": true,
	"TTL": true, "UDP": true, "UI": true, "UID": true, "UUID": true,
	"URI": true, "URL": true, "UTF8": true, "VM": true, "XML": true,
	"XMPP": true, "XSRF": true, "XSS": true,
}

// exportedName converts a metaModel lowerCamelCase property name (e.g.
// "languageId", "uri", "startCharacter") into an exported Go field name
// (e.g. "LanguageID", "URI", "StartCharacter"), applying the same
// initialism-uppercasing convention golint/revive expect (and that every
// hand-written pkg/lsp field already follows: URI not Uri, ID not Id).
func exportedName(propName string) string {
	words := splitCamelWords(propName)
	for i, w := range words {
		upper := strings.ToUpper(w)
		if commonInitialisms[upper] {
			words[i] = upper
			continue
		}
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, "")
}

// splitCamelWords splits lowerCamelCase / UpperCamelCase into its
// constituent words at each uppercase-letter boundary, e.g.
// "hierarchicalDocumentSymbolSupport" -> ["hierarchical", "Document",
// "Symbol", "Support"].
func splitCamelWords(s string) []string {
	var words []string
	var cur []rune
	for _, r := range s {
		if unicode.IsUpper(r) && len(cur) > 0 {
			words = append(words, string(cur))
			cur = nil
		}
		cur = append(cur, r)
	}
	if len(cur) > 0 {
		words = append(words, string(cur))
	}
	return words
}

// flattenProperties returns the fully-flattened property list for a
// metaModel structure: every "extends" ancestor's properties (recursively,
// in extends-list order), followed by name's own properties. "mixins" are
// deliberately never included — see the note in config.go.
func flattenProperties(name string, bn byName) ([]property, error) {
	s, ok := bn.structs[name]
	if !ok {
		return nil, fmt.Errorf("structure %q not found in metaModel", name)
	}
	var out []property
	for _, ex := range s.Extends {
		if ex.Kind != "reference" {
			return nil, fmt.Errorf("%s extends a non-reference type (kind=%q)", name, ex.Kind)
		}
		parent, err := flattenProperties(ex.Name, bn)
		if err != nil {
			return nil, fmt.Errorf("%s extends %s: %w", name, ex.Name, err)
		}
		out = append(out, parent...)
	}
	out = append(out, s.Properties...)
	return out, nil
}

func propNames(props []property) []string {
	names := make([]string, len(props))
	for i, p := range props {
		names[i] = p.Name
	}
	return names
}

// referenceKind reports whether name resolves to an enum (value semantics
// when optional) or a struct (pointer semantics when optional), consulting
// both the types this run is generating and the externalTypeKinds registry
// of hand-written types a generated field may still reference by name.
func referenceKind(name string, structNames, enumNames map[string]bool, external map[string]string) (kind string, ok bool) {
	if enumNames[name] {
		return "enum", true
	}
	if structNames[name] {
		return "struct", true
	}
	if k, ok2 := external[name]; ok2 {
		return k, true
	}
	return "", false
}

// resolveType mechanically maps a metaModel anyType to a Go type string,
// returning also the resolved "kind" (scalar/enum/struct/array) the caller
// uses to decide optional-pointer semantics. Kinds this generator does not
// support (map, or, and, tuple, literal, ...) are a hard error: every
// occurrence of one of those in the allowlisted structures' fields must be
// caught by an explicit fieldOverride in config.go instead (checked by the
// caller, resolveField, before this function is ever reached for such a
// field) — resolveType itself never guesses.
func resolveType(t anyType, structNames, enumNames map[string]bool, external map[string]string) (goType, kind string, err error) {
	switch t.Kind {
	case "base":
		gt, ok := baseTypeGo[t.Name]
		if !ok {
			return "", "", fmt.Errorf("unsupported base type %q (add to baseTypeGo or a fieldOverride)", t.Name)
		}
		return gt, "scalar", nil
	case "reference":
		k, ok := referenceKind(t.Name, structNames, enumNames, external)
		if !ok {
			return "", "", fmt.Errorf("unresolved reference %q (not in structConfigs, enumConfigs, or externalTypeKinds)", t.Name)
		}
		return t.Name, k, nil
	case "array":
		if t.Element == nil {
			return "", "", fmt.Errorf("array type missing element")
		}
		elemGoType, _, err := resolveType(*t.Element, structNames, enumNames, external)
		if err != nil {
			return "", "", fmt.Errorf("array element: %w", err)
		}
		return "[]" + elemGoType, "array", nil
	default:
		return "", "", fmt.Errorf("unsupported type kind %q (add a fieldOverride)", t.Kind)
	}
}

// resolveField computes the final Go type and json tag for one property,
// applying cfg's override for that property name if any (short-circuiting
// resolveType entirely — every override in config.go exists precisely
// because resolveType cannot, or should not, handle that field mechanically:
// an "or" type, or a reference to a type this generator deliberately doesn't
// model).
func resolveField(cfg structConfig, p property, structNames, enumNames map[string]bool, external map[string]string) (goType, tag string, needsJSON bool, err error) {
	if p.Optional {
		tag = p.Name + ",omitempty"
	} else {
		tag = p.Name
	}

	if ov, ok := cfg.overrides[p.Name]; ok {
		return ov.goType, tag, strings.Contains(ov.goType, "json."), nil
	}

	gt, kind, err := resolveType(p.Type, structNames, enumNames, external)
	if err != nil {
		return "", "", false, fmt.Errorf("%s.%s: %w", cfg.name, p.Name, err)
	}
	if kind == "struct" && p.Optional {
		gt = "*" + gt
	}
	return gt, tag, false, nil
}

func fieldDocFor(cfg structConfig, p property) string {
	if d, ok := cfg.fieldDoc[p.Name]; ok {
		return d
	}
	if ov, ok := cfg.overrides[p.Name]; ok {
		return ov.reason
	}
	return fmt.Sprintf("%s is the LSP metaModel %q property of %s.", exportedName(p.Name), p.Name, cfg.name)
}

func splitDoc(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// renderedField/renderedStruct/renderedEnumValue/renderedEnum are the
// template-ready, fully-resolved shapes fileTemplate renders — all semantic
// decisions (flatten, override, optional-pointer, name conversion) are made
// by Go code above; the template only prints.
type renderedField struct {
	Doc  []string
	Name string
	Type string
	Tag  string
}

type renderedStruct struct {
	Doc    []string
	Name   string
	Fields []renderedField
}

type renderedEnumValue struct {
	Name  string
	Value string
}

type renderedEnum struct {
	Doc        []string
	ConstDoc   []string
	Name       string
	Underlying string
	Values     []renderedEnumValue
}

func buildStruct(cfg structConfig, bn byName, structNames, enumNames map[string]bool, external map[string]string) (renderedStruct, bool, error) {
	flat, err := flattenProperties(cfg.name, bn)
	if err != nil {
		return renderedStruct{}, false, err
	}
	byPropName := make(map[string]property, len(flat))
	for _, p := range flat {
		byPropName[p.Name] = p
	}

	ordered := flat
	if cfg.onlyFields != nil {
		ordered = make([]property, 0, len(cfg.onlyFields))
		for _, want := range cfg.onlyFields {
			p, ok := byPropName[want]
			if !ok {
				return renderedStruct{}, false, fmt.Errorf(
					"%s: onlyFields references unknown property %q (flattened set: %v)",
					cfg.name, want, propNames(flat))
			}
			ordered = append(ordered, p)
		}
	}

	needsJSON := false
	rs := renderedStruct{Name: cfg.name, Doc: splitDoc(cfg.doc)}
	for _, p := range ordered {
		goType, tag, fNeedsJSON, err := resolveField(cfg, p, structNames, enumNames, external)
		if err != nil {
			return renderedStruct{}, false, err
		}
		needsJSON = needsJSON || fNeedsJSON
		rs.Fields = append(rs.Fields, renderedField{
			Doc:  splitDoc(fieldDocFor(cfg, p)),
			Name: exportedName(p.Name),
			Type: goType,
			Tag:  fmt.Sprintf(`json:"%s"`, tag),
		})
	}
	return rs, needsJSON, nil
}

func buildEnum(cfg enumConfig, bn byName) (renderedEnum, error) {
	e, ok := bn.enums[cfg.name]
	if !ok {
		return renderedEnum{}, fmt.Errorf("enumeration %q not found in metaModel", cfg.name)
	}
	var underlying string
	switch e.Type.Name {
	case "uinteger", "integer":
		underlying = "int"
	case "string":
		underlying = "string"
	default:
		return renderedEnum{}, fmt.Errorf("enumeration %s: unsupported underlying base type %q", cfg.name, e.Type.Name)
	}

	prefix := cfg.constPrefix
	if prefix == "" {
		prefix = cfg.name
	}

	re := renderedEnum{
		Name:       cfg.name,
		Underlying: underlying,
		Doc:        splitDoc(cfg.doc),
		ConstDoc:   splitDoc(cfg.constDoc),
	}
	for _, v := range e.Values {
		var lit string
		switch underlying {
		case "int":
			var n json.Number
			if err := json.Unmarshal(v.Value, &n); err != nil {
				return renderedEnum{}, fmt.Errorf("enumeration %s value %s: %w", cfg.name, v.Name, err)
			}
			lit = n.String()
		case "string":
			var sv string
			if err := json.Unmarshal(v.Value, &sv); err != nil {
				return renderedEnum{}, fmt.Errorf("enumeration %s value %s: %w", cfg.name, v.Name, err)
			}
			lit = strconv.Quote(sv)
		}
		re.Values = append(re.Values, renderedEnumValue{Name: prefix + v.Name, Value: lit})
	}
	return re, nil
}

type templateData struct {
	Version   string
	SHA256    string
	NeedsJSON bool
	Enums     []renderedEnum
	Structs   []renderedStruct
}

var funcMap = template.FuncMap{
	"bq": func() string { return "`" },
}

const fileTemplate = `// Code generated by cmd/lspgen from the pinned LSP {{.Version}} metaModel.json; DO NOT EDIT.
//
// Regenerate:    go generate ./pkg/lsp/...
// Source:        https://raw.githubusercontent.com/microsoft/language-server-protocol/gh-pages/_specifications/lsp/{{.Version}}/metaModel/metaModel.json
// Pinned copy:   cmd/lspgen/metaModel.json (sha256 {{.SHA256}})
// Allowlist:     cmd/lspgen/config.go (also documents every excluded/hand-written type)

package lsp
{{if .NeedsJSON}}
import "encoding/json"
{{end}}
{{range .Enums}}
{{$enum := .}}
{{range .Doc}}// {{.}}
{{end}}type {{.Name}} {{.Underlying}}

{{range .ConstDoc}}// {{.}}
{{end}}const (
{{range .Values}}	{{.Name}} {{$enum.Name}} = {{.Value}}
{{end}})
{{end}}
{{range .Structs}}
{{range .Doc}}// {{.}}
{{end}}type {{.Name}} struct {
{{range $i, $f := .Fields}}{{if $i}}
{{end}}{{range $f.Doc}}	// {{.}}
{{end}}	{{$f.Name}} {{$f.Type}} {{bq}}{{$f.Tag}}{{bq}}
{{end}}}
{{end}}
`

// Generate parses metaModelData (the pinned metaModel.json) and renders the
// allowlisted structures/enumerations from config.go into gofmt-formatted Go
// source for package lsp. It is pure and deterministic: identical input
// always produces byte-identical output.
func Generate(metaModelData []byte) ([]byte, error) {
	var mm metaModel
	if err := json.Unmarshal(metaModelData, &mm); err != nil {
		return nil, fmt.Errorf("parse metaModel.json: %w", err)
	}
	bn := newByName(&mm)

	structNames := make(map[string]bool, len(structConfigs))
	for _, c := range structConfigs {
		structNames[c.name] = true
	}
	enumNames := make(map[string]bool, len(enumConfigs))
	for _, c := range enumConfigs {
		enumNames[c.name] = true
	}

	data := templateData{Version: mm.MetaData.Version, SHA256: metaModelSHA256}

	for _, ec := range enumConfigs {
		re, err := buildEnum(ec, bn)
		if err != nil {
			return nil, err
		}
		data.Enums = append(data.Enums, re)
	}
	for _, sc := range structConfigs {
		rs, needsJSON, err := buildStruct(sc, bn, structNames, enumNames, externalTypeKinds)
		if err != nil {
			return nil, err
		}
		data.NeedsJSON = data.NeedsJSON || needsJSON
		data.Structs = append(data.Structs, rs)
	}

	tmpl, err := template.New("types_gen").Funcs(funcMap).Parse(fileTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt generated source: %w\n--- raw template output ---\n%s", err, buf.String())
	}
	return formatted, nil
}
