package gts

import (
	"sort"
	"strings"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/lspbridge/pkg/lspserver"
)

// TokenTypes is the semantic-token legend. Highlight capture names (the
// tree-sitter convention: "keyword.control", "function.method", ...) are
// folded onto these standard LSP types by captureType.
var TokenTypes = []string{
	"namespace", "type", "class", "enum", "interface", "struct", "typeParameter",
	"parameter", "variable", "property", "enumMember", "event", "function",
	"method", "macro", "keyword", "modifier", "comment", "string", "number",
	"regexp", "operator", "decorator",
}

// TokenModifiers is the modifier legend (bit i = TokenModifiers[i]).
var TokenModifiers = []string{"defaultLibrary", "readonly", "documentation"}

const (
	modDefaultLibrary = 1 << iota
	modReadonly
	modDocumentation
)

var typeIndex = func() map[string]int {
	m := map[string]int{}
	for i, t := range TokenTypes {
		m[t] = i
	}
	return m
}()

// captureType maps a highlight capture name to (token type index, modifiers);
// ok is false for captures that carry no useful semantic token (punctuation,
// spell/nospell hints, conceal, errors).
func captureType(name string) (typ int, mods int, ok bool) {
	name = strings.TrimPrefix(name, "@")
	parts := strings.Split(name, ".")
	for _, p := range parts[1:] {
		switch p {
		case "builtin":
			mods |= modDefaultLibrary
		case "documentation", "doc":
			mods |= modDocumentation
		}
	}
	pick := func(t string) (int, int, bool) { return typeIndex[t], mods, true }
	has := func(p string) bool {
		for _, x := range parts {
			if x == p {
				return true
			}
		}
		return false
	}
	switch parts[0] {
	case "keyword", "conditional", "repeat", "include", "exception", "storageclass",
		"preproc", "define", "return", "boolean", "debug", "import", "storage", "label":
		if parts[0] == "keyword" && has("operator") {
			return pick("operator")
		}
		if parts[0] == "keyword" && has("function") {
			return pick("keyword")
		}
		return pick("keyword")
	case "string", "character", "text", "markup":
		switch {
		case has("regex") || has("regexp"):
			return pick("regexp")
		case parts[0] == "markup" && (has("heading") || has("title")):
			return pick("type")
		case parts[0] == "text" && has("title"):
			return pick("type")
		case parts[0] == "markup" && (has("italic") || has("strong") || has("underline") || has("strikethrough") || has("quote") || has("list") || has("math")):
			return 0, 0, false
		case parts[0] == "text" && (has("emphasis") || has("strong") || has("underline") || has("danger") || has("warning")):
			return 0, 0, false
		}
		return pick("string")
	case "comment":
		return pick("comment")
	case "number", "float":
		return pick("number")
	case "operator":
		return pick("operator")
	case "function", "method", "constructor":
		switch {
		case has("macro"):
			return pick("macro")
		case parts[0] == "method" || has("method") || has("member"):
			return pick("method")
		case parts[0] == "constructor":
			return pick("class")
		}
		return pick("function")
	case "type":
		if has("parameter") {
			return pick("typeParameter")
		}
		if has("enum") && has("variant") {
			return pick("enumMember")
		}
		if has("enum") {
			return pick("enum")
		}
		return pick("type")
	case "variable":
		if has("parameter") {
			return pick("parameter")
		}
		if has("member") {
			return pick("property")
		}
		return pick("variable")
	case "parameter":
		return pick("parameter")
	case "property", "field":
		return pick("property")
	case "attribute", "annotation", "decorator":
		return pick("decorator")
	case "constant":
		if has("macro") {
			return pick("macro")
		}
		if has("character") || has("string") {
			return pick("string")
		}
		if has("numeric") || has("float") || has("integer") {
			return pick("number")
		}
		if has("boolean") || has("null") || has("language") {
			return pick("keyword")
		}
		mods |= modReadonly
		return pick("variable")
	case "module", "namespace":
		return pick("namespace")
	case "tag":
		if has("attribute") {
			return pick("property")
		}
		if has("delimiter") || has("error") {
			return 0, 0, false
		}
		return pick("type")
	case "symbol":
		return pick("enumMember")
	case "escape":
		return pick("string")
	case "identifier":
		return pick("variable")
	}
	return 0, 0, false
}

type rawToken struct {
	start, end int // byte offsets
	typ, mods  int
}

// encodeTokens turns sorted byte-range tokens into the LSP delta encoding:
// 5 ints per token (deltaLine, deltaStartChar, length, type, modifiers), all
// positions and lengths in UTF-16 code units. A token that spans lines is
// split into one token per line (the client capability multilineTokenSupport
// is not assumed). Overlaps are clipped (an earlier token wins its bytes) and
// empty tokens dropped, so the stream is strictly ordered.
func encodeTokens(d *lspserver.Doc, toks []rawToken) []int {
	sort.SliceStable(toks, func(i, j int) bool { return toks[i].start < toks[j].start })
	data := make([]int, 0, len(toks)*5)
	prevLine, prevChar, cursor := 0, 0, 0
	emit := func(line, char, length, typ, mods int) {
		dl := line - prevLine
		dc := char
		if dl == 0 {
			dc = char - prevChar
		}
		data = append(data, dl, dc, length, typ, mods)
		prevLine, prevChar = line, char
	}
	text := d.Text
	for _, t := range toks {
		s, e := t.start, t.end
		if s < cursor {
			s = cursor
		}
		if e > len(text) {
			e = len(text)
		}
		if s >= e {
			continue
		}
		cursor = e
		for s < e {
			// Cut the token at the next line break.
			nl := s
			for nl < e && text[nl] != '\n' {
				nl++
			}
			segEnd := nl
			if segEnd > s && text[segEnd-1] == '\r' {
				segEnd--
			}
			if segEnd > s {
				p := d.Position(s)
				emit(p.Line, p.Character, lspserver.UTF16Len(text[s:segEnd]), t.typ, t.mods)
			}
			s = nl + 1 // skip the '\n'
		}
	}
	return data
}

// DecodeTokens expands delta-encoded data into absolute (line, char, length,
// type, modifiers) tuples. Exported for tests and tooling.
func DecodeTokens(data []int) [][5]int {
	var out [][5]int
	line, char := 0, 0
	for i := 0; i+4 < len(data); i += 5 {
		line += data[i]
		if data[i] == 0 {
			char += data[i+1]
		} else {
			char = data[i+1]
		}
		out = append(out, [5]int{line, char, data[i+2], data[i+3], data[i+4]})
	}
	return out
}

// Legend is the legend sent in the initialize response.
func Legend() lsp.SemanticTokensLegend {
	return lsp.SemanticTokensLegend{TokenTypes: TokenTypes, TokenModifiers: TokenModifiers}
}
