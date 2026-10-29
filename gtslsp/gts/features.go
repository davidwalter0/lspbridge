package gts

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/davidwalter0/gotreesitter"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/lspbridge/pkg/lspserver"
)

// snapshot returns a doc state's language, document and tree with l.mu HELD;
// the caller must unlock (via the returned func). ok is false when there is
// nothing to answer from (unknown document, no grammar, failed parse).
func (s *Server) snapshot(uri lsp.DocumentURI) (st *docState, unlock func(), ok bool) {
	st = s.state(uri)
	if st == nil || st.lang == nil {
		return st, func() {}, false
	}
	st.lang.mu.Lock()
	if st.tree == nil || st.tree.RootNode() == nil {
		st.lang.mu.Unlock()
		return st, func() {}, false
	}
	return st, st.lang.mu.Unlock, true
}

func rangeOf(d *lspserver.Doc, start, end uint32) lsp.Range {
	return d.Range(int(start), int(end))
}

// syntaxDiagnostics reports ERROR and MISSING nodes. An ERROR subtree is one
// diagnostic (its inner nodes are recovery debris), MISSING is a zero-width
// node the parser inserted. Caller holds lang.mu.
func (s *Server) syntaxDiagnostics(st *docState) []lsp.Diagnostic {
	diags := []lsp.Diagnostic{}
	if st.parseErr != "" {
		return append(diags, lsp.Diagnostic{Severity: lsp.SeverityWarning, Source: "gts",
			Message: "parse failed: " + st.parseErr})
	}
	if st.tree == nil {
		return diags
	}
	root := st.tree.RootNode()
	if st.stopped {
		diags = append(diags, lsp.Diagnostic{Severity: lsp.SeverityWarning, Source: "gts",
			Message: "parse stopped early (timeout or safety limit); syntax results are partial"})
	}
	if root == nil || !root.HasErrorOrMissing() {
		return diags
	}
	lang := st.lang.lang
	stack := []*gotreesitter.Node{root}
	var found []lsp.Diagnostic
	for len(stack) > 0 && len(found) < maxSyntaxDiags {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch {
		case n.IsMissing():
			found = append(found, lsp.Diagnostic{
				Range: rangeOf(st.doc, n.StartByte(), n.EndByte()), Severity: lsp.SeverityError,
				Source: "gts", Message: "missing " + describe(n.Type(lang)),
			})
			continue
		case n.IsError():
			msg := "syntax error"
			if txt := strings.TrimSpace(n.Text(st.view)); txt != "" && !strings.ContainsAny(txt, "\n") && len(txt) <= 40 {
				msg = "syntax error near " + quote(txt)
			}
			found = append(found, lsp.Diagnostic{
				Range: rangeOf(st.doc, n.StartByte(), n.EndByte()), Severity: lsp.SeverityError,
				Source: "gts", Message: msg,
			})
			continue
		}
		if !n.HasErrorOrMissing() {
			continue
		}
		// Push children reversed so they pop in source order.
		for i := n.ChildCount() - 1; i >= 0; i-- {
			if c := n.Child(i); c != nil {
				stack = append(stack, c)
			}
		}
	}
	return append(diags, found...)
}

func describe(t string) string {
	if t == "" {
		return "token"
	}
	return quote(t)
}

func quote(s string) string { return "\"" + s + "\"" }

// ---- documentSymbol ---------------------------------------------------

var symbolKinds = map[string]lsp.SymbolKind{
	"function":    lsp.SymbolKindFunction,
	"method":      lsp.SymbolKindMethod,
	"class":       lsp.SymbolKindClass,
	"interface":   lsp.SymbolKindInterface,
	"type":        lsp.SymbolKindStruct,
	"constructor": lsp.SymbolKindConstructor,
	"constant":    lsp.SymbolKindConstant,
	"variable":    lsp.SymbolKindVariable,
	"module":      lsp.SymbolKindModule,
	"enum":        lsp.SymbolKindEnum,
	"record":      lsp.SymbolKindStruct,
	"field":       lsp.SymbolKindField,
	"property":    lsp.SymbolKindProperty,
	"namespace":   lsp.SymbolKindNamespace,
	"key":         lsp.SymbolKindKey,
	"string":      lsp.SymbolKindString,
	"heading":     lsp.SymbolKindString,
	"target":      lsp.SymbolKindFunction,
}

func (s *Server) documentSymbols(uri lsp.DocumentURI) []lsp.DocumentSymbol {
	out := []lsp.DocumentSymbol{}
	st, unlock, ok := s.snapshot(uri)
	defer unlock()
	if !ok {
		return out
	}
	_, ol := st.lang.tagEngines()
	if ol == nil {
		return out
	}
	syms, _ := ol.OutlineTree(st.tree)
	var conv func(in []gotreesitter.OutlineSymbol) []lsp.DocumentSymbol
	conv = func(in []gotreesitter.OutlineSymbol) []lsp.DocumentSymbol {
		var res []lsp.DocumentSymbol
		for _, o := range in {
			if strings.TrimSpace(o.Name) == "" {
				continue
			}
			kind, known := symbolKinds[o.Kind]
			if !known {
				kind = lsp.SymbolKindVariable
			}
			r := rangeOf(st.doc, o.Range.StartByte, o.Range.EndByte)
			sel := rangeOf(st.doc, o.NameRange.StartByte, o.NameRange.EndByte)
			res = append(res, lsp.DocumentSymbol{
				Name: o.Name, Detail: o.Owner, Kind: kind, Range: r, SelectionRange: sel,
				Children: conv(o.Children),
			})
		}
		return res
	}
	if r := conv(syms); r != nil {
		return r
	}
	return out
}

// ---- foldingRange -------------------------------------------------------

func (s *Server) foldingRanges(uri lsp.DocumentURI) []lsp.FoldingRange {
	out := []lsp.FoldingRange{}
	st, unlock, ok := s.snapshot(uri)
	defer unlock()
	if !ok {
		return out
	}
	lang := st.lang.lang
	root := st.tree.RootNode()
	seen := map[int]bool{} // start line -> already folded (outermost wins)
	stack := []*gotreesitter.Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n != root && n.IsNamed() && !n.IsMissing() {
			sp, ep := n.StartPoint(), n.EndPoint()
			startLine, endLine := int(sp.Row), int(ep.Row)
			if endLine > startLine && !seen[startLine] {
				// A node ending on a line that holds only closing delimiters
				// folds up to the line before it, so the closer stays visible.
				if ep.Column > 0 && onlyClosers(lineBytes(st.view, endLine, int(ep.Column))) {
					endLine--
				}
				if endLine > startLine {
					seen[startLine] = true
					fr := lsp.FoldingRange{StartLine: startLine, EndLine: endLine}
					if strings.Contains(n.Type(lang), "comment") {
						fr.Kind = lsp.FoldingRangeKindComment
					}
					out = append(out, fr)
				}
			}
		}
		for i := n.ChildCount() - 1; i >= 0; i-- {
			if c := n.Child(i); c != nil {
				stack = append(stack, c)
			}
		}
	}
	sortFolds(out)
	return out
}

func sortFolds(f []lsp.FoldingRange) {
	for i := 1; i < len(f); i++ {
		for j := i; j > 0 && f[j].StartLine < f[j-1].StartLine; j-- {
			f[j], f[j-1] = f[j-1], f[j]
		}
	}
}

// lineBytes returns the first col bytes of line number line (0-based).
func lineBytes(src []byte, line, col int) []byte {
	off := 0
	for l := 0; l < line; l++ {
		i := indexByte(src[off:], '\n')
		if i < 0 {
			return nil
		}
		off += i + 1
	}
	end := off + col
	if end > len(src) {
		end = len(src)
	}
	return src[off:end]
}

func onlyClosers(b []byte) bool {
	t := strings.TrimSpace(string(b))
	if t == "" {
		return false
	}
	for _, r := range t {
		switch r {
		case '}', ')', ']', ';', ',':
		default:
			return false
		}
	}
	return true
}

// ---- selectionRange -----------------------------------------------------

func (s *Server) selectionRanges(uri lsp.DocumentURI, positions []lsp.Position) []lsp.SelectionRange {
	out := make([]lsp.SelectionRange, 0, len(positions))
	st, unlock, ok := s.snapshot(uri)
	defer unlock()
	for _, pos := range positions {
		if !ok {
			out = append(out, lsp.SelectionRange{Range: lsp.Range{Start: pos, End: pos}})
			continue
		}
		off := uint32(st.doc.Offset(pos))
		n := st.tree.RootNode().DescendantForByteRange(off, off)
		if n == nil {
			n = st.tree.RootNode()
		}
		// Collect inner -> outer, dropping nodes that do not grow the range.
		var chain []lsp.Range
		for ; n != nil; n = n.Parent() {
			r := rangeOf(st.doc, n.StartByte(), n.EndByte())
			if len(chain) > 0 && chain[len(chain)-1] == r {
				continue
			}
			chain = append(chain, r)
		}
		if len(chain) == 0 {
			chain = append(chain, lsp.Range{Start: pos, End: pos})
		}
		var sel *lsp.SelectionRange
		for i := len(chain) - 1; i >= 0; i-- { // build outermost first
			sel = &lsp.SelectionRange{Range: chain[i], Parent: sel}
		}
		out = append(out, *sel)
	}
	return out
}

// ---- semanticTokens -----------------------------------------------------

func (s *Server) semanticTokens(uri lsp.DocumentURI) lsp.SemanticTokens {
	res := lsp.SemanticTokens{Data: []int{}}
	st, unlock, ok := s.snapshot(uri)
	defer unlock()
	if !ok {
		return res
	}
	hl := st.lang.highlighter()
	if hl == nil {
		return res
	}
	ranges := hl.HighlightTreeRange(st.tree, st.view, 0, uint32(len(st.view)))
	toks := make([]rawToken, 0, len(ranges))
	for _, r := range ranges {
		typ, mods, ok := captureType(r.Capture)
		if !ok {
			continue
		}
		toks = append(toks, rawToken{start: int(r.StartByte), end: int(r.EndByte), typ: typ, mods: mods})
	}
	res.Data = encodeTokens(st.doc, toks)
	return res
}

// ---- tags, definition, references (HEURISTIC) ---------------------------

// tagsOf returns the (cached) tags of st. Caller holds st.lang.mu.
func (s *Server) tagsOf(st *docState) []gotreesitter.Tag {
	if st.tagsFor == st.doc.Version && st.tags != nil {
		return st.tags
	}
	tg, _ := st.lang.tagEngines()
	if tg == nil || st.tree == nil {
		return nil
	}
	st.tags = tg.TagTree(st.tree)
	if st.tags == nil {
		st.tags = []gotreesitter.Tag{}
	}
	st.tagsFor = st.doc.Version
	return st.tags
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// wordAt returns the identifier-like word under off and its byte span.
func wordAt(text []byte, off int) (string, int, int) {
	if off > len(text) {
		off = len(text)
	}
	start := off
	for start > 0 {
		r, size := utf8.DecodeLastRune(text[:start])
		if !isWordRune(r) {
			break
		}
		start -= size
	}
	end := off
	for end < len(text) {
		r, size := utf8.DecodeRune(text[end:])
		if !isWordRune(r) {
			break
		}
		end += size
	}
	return string(text[start:end]), start, end
}

// findTags collects, across every open document with a grammar, the tags
// named name that satisfy keep. Locks are taken one language at a time.
func (s *Server) findTags(name string, keep func(kind string) bool) []lsp.Location {
	s.mu.Lock()
	states := make([]*docState, 0, len(s.states))
	for _, st := range s.states {
		states = append(states, st)
	}
	s.mu.Unlock()
	sortStates(states)
	locs := []lsp.Location{}
	for _, st := range states {
		if st.lang == nil {
			continue
		}
		st.lang.mu.Lock()
		for _, t := range s.tagsOf(st) {
			if t.Name == name && keep(t.Kind) {
				locs = append(locs, lsp.Location{URI: st.uri,
					Range: rangeOf(st.doc, t.NameRange.StartByte, t.NameRange.EndByte)})
			}
		}
		st.lang.mu.Unlock()
	}
	return locs
}

func sortStates(a []*docState) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j].uri < a[j-1].uri; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func (s *Server) wordUnder(uri lsp.DocumentURI, pos lsp.Position) string {
	st := s.state(uri)
	if st == nil {
		return ""
	}
	m := s.lockOf(st)
	m.Lock()
	text, off := st.doc.Text, st.doc.Offset(pos)
	m.Unlock()
	w, _, _ := wordAt(text, off)
	return w
}

// definition resolves by NAME: every definition.* tag spelled like the word
// under the cursor in any open document. It does not understand scopes,
// imports or overloads, which is why the server labels it HEURISTIC.
func (s *Server) definition(uri lsp.DocumentURI, pos lsp.Position) []lsp.Location {
	w := s.wordUnder(uri, pos)
	if w == "" {
		return []lsp.Location{}
	}
	return s.findTags(w, func(k string) bool { return strings.HasPrefix(k, "definition.") })
}

// references is the same name match over definition.* and reference.* tags.
func (s *Server) references(uri lsp.DocumentURI, pos lsp.Position, includeDecl bool) []lsp.Location {
	w := s.wordUnder(uri, pos)
	if w == "" {
		return []lsp.Location{}
	}
	return s.findTags(w, func(k string) bool {
		if strings.HasPrefix(k, "reference.") {
			return true
		}
		return includeDecl && strings.HasPrefix(k, "definition.")
	})
}

// ---- completion (HEURISTIC) ---------------------------------------------

var tagKindToCompletion = map[string]lsp.CompletionItemKind{
	"function": lsp.CompletionItemKindFunction, "method": lsp.CompletionItemKindMethod,
	"class": lsp.CompletionItemKindClass, "type": lsp.CompletionItemKindStruct,
	"interface": lsp.CompletionItemKindInterface, "constant": lsp.CompletionItemKindConstant,
	"module": lsp.CompletionItemKindModule, "variable": lsp.CompletionItemKindVariable,
	"constructor": lsp.CompletionItemKindConstructor, "enum": lsp.CompletionItemKindEnum,
}

const maxCompletions = 500

// completion offers the grammar's keywords, the document's definition names
// and its identifier-like leaf tokens, filtered by the typed prefix. It knows
// nothing about scope or types.
func (s *Server) completion(uri lsp.DocumentURI, pos lsp.Position) lsp.CompletionList {
	list := lsp.CompletionList{Items: []lsp.CompletionItem{}}
	st := s.state(uri)
	if st == nil || st.lang == nil {
		return list
	}
	st.lang.mu.Lock()
	defer st.lang.mu.Unlock()
	off := st.doc.Offset(pos)
	prefix, wstart, _ := wordAt(st.doc.Text, off)
	prefix = prefix[:max(0, off-wstart)]
	seen := map[string]bool{}
	add := func(label string, kind lsp.CompletionItemKind, detail, sort string) {
		if label == "" || seen[label] || len(list.Items) >= maxCompletions || !strings.HasPrefix(label, prefix) || label == prefix {
			return
		}
		seen[label] = true
		list.Items = append(list.Items, lsp.CompletionItem{Label: label, Kind: kind, Detail: detail, SortText: sort + label})
	}
	if st.tree != nil {
		for _, t := range s.tagsOf(st) {
			if k, ok := strings.CutPrefix(t.Kind, "definition."); ok {
				ck, known := tagKindToCompletion[k]
				if !known {
					ck = lsp.CompletionItemKindVariable
				}
				add(t.Name, ck, k+" (this file)", "1")
			}
		}
		// Identifier-like leaves: named, childless, word-shaped.
		lang := st.lang.lang
		stack := []*gotreesitter.Node{st.tree.RootNode()}
		for len(stack) > 0 && len(list.Items) < maxCompletions {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if n.ChildCount() == 0 {
				if n.IsNamed() && strings.Contains(n.Type(lang), "identifier") {
					if txt := n.Text(st.view); wordRE.MatchString(txt) && (off < int(n.StartByte()) || off > int(n.EndByte())) {
						add(txt, lsp.CompletionItemKindVariable, "identifier (this file)", "2")
					}
				}
				continue
			}
			for i := n.ChildCount() - 1; i >= 0; i-- {
				if c := n.Child(i); c != nil {
					stack = append(stack, c)
				}
			}
		}
	}
	for _, k := range st.lang.Keywords() {
		add(k, lsp.CompletionItemKindKeyword, "keyword", "3")
	}
	return list
}
