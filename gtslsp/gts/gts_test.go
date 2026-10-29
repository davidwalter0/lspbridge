package gts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/lspbridge/pkg/lspserver"
)

// harness opens documents on a Server without a transport.
type harness struct {
	t     *testing.T
	s     *Server
	store *lspserver.Store
}

func newHarness(t *testing.T) *harness {
	return &harness{t: t, s: New(Config{}), store: lspserver.NewStore()}
}

func (h *harness) open(path, text string) *docState {
	uri := lspserver.PathToURI(path)
	d := h.store.Open(lsp.TextDocumentItem{URI: uri, Version: 1, Text: text})
	st := &docState{uri: uri, doc: d, tagsFor: -1, ext: map[string][]lsp.Diagnostic{}}
	st.lang = h.s.cfg.Hooks.Detect(d.Path, "", d.Text)
	h.s.mu.Lock()
	h.s.states[uri] = st
	h.s.mu.Unlock()
	h.s.reparse(st, d, nil)
	return st
}

func (h *harness) openFile(f string) *docState {
	src, err := os.ReadFile(f)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.open(f, string(src))
}

func (h *harness) change(st *docState, version int, changes ...lsp.TextDocumentContentChangeEvent) {
	ch := h.store.Apply(lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: st.uri, Version: version},
		ContentChanges: changes,
	})
	h.s.reparse(st, ch.New, ch)
}

func rg(l1, c1, l2, c2 int) *lsp.Range {
	return &lsp.Range{Start: lsp.Position{Line: l1, Character: c1}, End: lsp.Position{Line: l2, Character: c2}}
}

func fixtures(t *testing.T) []string {
	files, err := filepath.Glob("../testdata/*/*/*")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	return files
}

func TestFixtureDiagnostics(t *testing.T) {
	h := newHarness(t)
	for _, f := range fixtures(t) {
		st := h.openFile(f)
		if st.lang == nil {
			t.Errorf("%s: no language detected", f)
			continue
		}
		kind := filepath.Base(filepath.Dir(f))
		lang := filepath.Base(filepath.Dir(filepath.Dir(f)))
		n := len(st.syntaxDiag)
		switch {
		case kind == "clean" && n != 0:
			t.Errorf("%s: clean fixture has %d diagnostics: %+v", f, n, st.syntaxDiag)
		case kind == "broken" && lang == "markdown":
			// CommonMark has no syntax errors: every input parses.
			if n != 0 {
				t.Errorf("%s: markdown cannot be broken, got %+v", f, st.syntaxDiag)
			}
		case kind == "broken" && n == 0:
			t.Errorf("%s: broken fixture has no diagnostics", f)
		}
		for _, d := range st.syntaxDiag {
			if d.Source != "gts" || d.Severity != lsp.SeverityError && d.Severity != lsp.SeverityWarning {
				t.Errorf("%s: bad diagnostic %+v", f, d)
			}
		}
		if st.lang.Name != lang {
			t.Errorf("%s: detected %q", f, st.lang.Name)
		}
	}
}

// TestIncrementalMatchesFull edits each clean fixture in place (delete a
// span, reinsert it, then break and repair it) and requires the incremental
// tree to print exactly as a fresh parse of the same text.
func TestIncrementalMatchesFull(t *testing.T) {
	h := newHarness(t)
	for _, f := range fixtures(t) {
		if filepath.Base(filepath.Dir(f)) != "clean" {
			continue
		}
		st := h.openFile(f)
		text := string(st.doc.Text)
		lines := strings.Split(text, "\n")
		if len(lines) < 6 {
			continue
		}
		mid := len(lines) / 2
		cut := lines[mid]
		steps := [][]lsp.TextDocumentContentChangeEvent{
			{{Range: rg(mid, 0, mid+1, 0), Text: ""}},                               // delete a line
			{{Range: rg(mid, 0, mid, 0), Text: cut + "\n"}},                         // put it back
			{{Range: rg(mid, 0, mid, 0), Text: "(("}},                               // break it
			{{Range: rg(mid, 0, mid, 2), Text: ""}},                                 // repair it
			{{Range: rg(0, 0, 0, 0), Text: "😀"}, {Range: rg(0, 0, 0, 2), Text: ""}}, // emoji in, out
		}
		for i, step := range steps {
			h.change(st, i+2, step...)
			fresh := h.open(filepath.Join("/fresh", filepath.Base(f)), string(st.doc.Text))
			if st.tree == nil || fresh.tree == nil {
				t.Errorf("%s step %d: tree nil (incremental err %q, fresh err %q)", f, i, st.parseErr, fresh.parseErr)
				continue
			}
			st.lang.mu.Lock()
			got, want := st.tree.RootNode().SExpr(st.lang.lang), fresh.tree.RootNode().SExpr(fresh.lang.lang)
			st.lang.mu.Unlock()
			if got != want {
				t.Errorf("%s step %d: incremental tree differs from fresh parse\n got: %.300s\nwant: %.300s", f, i, got, want)
			}
			if len(st.syntaxDiag) != len(fresh.syntaxDiag) {
				t.Errorf("%s step %d: %d diagnostics vs %d fresh", f, i, len(st.syntaxDiag), len(fresh.syntaxDiag))
			}
		}
		if string(st.doc.Text) != text {
			t.Errorf("%s: text not restored after edit round trip", f)
		}
	}
}

func TestSemanticTokensWithEmojiAndMultiline(t *testing.T) {
	h := newHarness(t)
	src := "package p\n\n/* a\n b */\nvar s = \"😀\"; var y = 12\n"
	st := h.open("/x/p.go", src)
	tok := h.s.semanticTokens(st.uri)
	if len(tok.Data) == 0 || len(tok.Data)%5 != 0 {
		t.Fatalf("data = %v", tok.Data)
	}
	lines := strings.Split(src, "\n")
	prevLine, prevEnd := -1, 0
	byText := map[string]bool{}
	for _, d := range DecodeTokens(tok.Data) {
		line, char, length := d[0], d[1], d[2]
		if d[3] < 0 || d[3] >= len(TokenTypes) {
			t.Fatalf("type index out of legend: %v", d)
		}
		u16 := utf16Units(lines[line])
		if char < 0 || char+length > len(u16) || length <= 0 {
			t.Fatalf("token %v outside line %q (%d units)", d, lines[line], len(u16))
		}
		if line == prevLine && char < prevEnd {
			t.Errorf("overlapping token %v (previous ended at %d)", d, prevEnd)
		}
		if line < prevLine {
			t.Errorf("tokens out of order: %v", d)
		}
		prevLine, prevEnd = line, char+length
		byText[string(decodeUTF16(u16[char:char+length]))+"|"+TokenTypes[d[3]]] = true
	}
	for _, want := range []string{"package|keyword", "var|keyword", "12|number", "/* a|comment", " b */|comment"} {
		if !byText[want] {
			t.Errorf("missing token %q in %v", want, byText)
		}
	}
	// y sits after the 2-unit emoji: its column must be in UTF-16, not bytes
	// or runes. Line 4: var s = "😀"; var y = 12
	found := false
	for _, d := range DecodeTokens(tok.Data) {
		if d[0] == 4 && string(decodeUTF16(utf16Units(lines[4])[d[1]:d[1]+d[2]])) == "y" {
			found = true
		}
	}
	if !found {
		t.Errorf("no token resolved to identifier y on the emoji line: %v", DecodeTokens(tok.Data))
	}
}

func TestSelectionRangeNestsAndContains(t *testing.T) {
	h := newHarness(t)
	st := h.openFile("../testdata/go/clean/sample.go")
	// `fmt` in `fmt.Sprintf` on line 19.
	res := h.s.selectionRanges(st.uri, []lsp.Position{{Line: 19, Character: 10}})
	if len(res) != 1 {
		t.Fatalf("got %d", len(res))
	}
	depth := 0
	for sr := &res[0]; sr != nil; sr = sr.Parent {
		depth++
		if sr.Parent != nil {
			p := sr.Parent.Range
			if before(sr.Range.Start, p.Start) || before(p.End, sr.Range.End) {
				t.Errorf("parent %v does not contain child %v", p, sr.Range)
			}
			if p == sr.Range {
				t.Errorf("parent repeats child range %v", p)
			}
		}
	}
	if depth < 4 {
		t.Errorf("selection chain too shallow: %d", depth)
	}
	if res[0].Range.Start.Line != 19 {
		t.Errorf("innermost range not on line 19: %v", res[0].Range)
	}
}

func before(a, b lsp.Position) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character
}

func TestFoldingOnePerStartLine(t *testing.T) {
	h := newHarness(t)
	st := h.openFile("../testdata/go/clean/sample.go")
	folds := h.s.foldingRanges(st.uri)
	seen := map[int]bool{}
	for _, f := range folds {
		if f.EndLine <= f.StartLine {
			t.Errorf("empty fold %+v", f)
		}
		if seen[f.StartLine] {
			t.Errorf("two folds start on line %d", f.StartLine)
		}
		seen[f.StartLine] = true
	}
	// func Speak (line 18) ends with `}` on line 20: folds 18..19.
	ok := false
	for _, f := range folds {
		if f.StartLine == 18 && f.EndLine == 19 {
			ok = true
		}
	}
	if !ok {
		t.Errorf("no 18..19 fold for the method body in %+v", folds)
	}
}

func TestCppTypeUseIsNotASymbol(t *testing.T) {
	h := newHarness(t)
	st := h.openFile("../testdata/cpp/clean/main.cpp")
	var names func([]lsp.DocumentSymbol) []string
	names = func(in []lsp.DocumentSymbol) (out []string) {
		for _, s := range in {
			out = append(out, s.Name)
			out = append(out, names(s.Children)...)
		}
		return
	}
	got := names(h.s.documentSymbols(st.uri))
	count := map[string]int{}
	for _, n := range got {
		count[n]++
	}
	// `struct shapes::Circle first` and `const std::vector<shapes::Circle>`
	// are USES of Circle inside total(); only the one body defines it.
	if count["Circle"] != 2 { // the struct and its constructor
		t.Errorf("Circle appears %d times, want 2 (struct + constructor): %v", count["Circle"], got)
	}
	for n, c := range count {
		if c > 2 {
			t.Errorf("%q duplicated %d times: %v", n, c, got)
		}
	}
	// no symbol may be nested in the function total (its body only uses types)
	for _, s := range h.s.documentSymbols(st.uri) {
		if s.Name == "total" && len(s.Children) != 0 {
			t.Errorf("total has children %v", names(s.Children))
		}
	}
}

func TestDefinitionReferencesAcrossOpenDocs(t *testing.T) {
	h := newHarness(t)
	a := h.open("/w/a.go", "package p\n\nfunc Helper() int { return 1 }\n")
	b := h.open("/w/b.go", "package p\n\nfunc use() int { return Helper() + Helper() }\n")
	// cursor on a call to Helper in b.go
	pos := lsp.Position{Line: 2, Character: strings.Index("func use() int { return Helper() + Helper() }", "Helper") + 2}
	defs := h.s.definition(b.uri, pos)
	if len(defs) != 1 || defs[0].URI != a.uri || defs[0].Range.Start.Line != 2 || defs[0].Range.Start.Character != 5 {
		t.Fatalf("definition = %+v", defs)
	}
	refs := h.s.references(b.uri, pos, false)
	if len(refs) != 2 {
		t.Fatalf("references (no decl) = %+v", refs)
	}
	refs = h.s.references(b.uri, pos, true)
	if len(refs) != 3 {
		t.Fatalf("references (with decl) = %+v", refs)
	}
	if e := h.s.definition(b.uri, lsp.Position{Line: 0, Character: 0}); len(e) != 0 {
		t.Errorf("`package` has no definition, got %+v", e)
	}
	// unknown document: empty, not nil/error
	if r := h.s.references("file:///nope", pos, true); r == nil || len(r) != 0 {
		t.Errorf("unknown doc references = %#v", r)
	}
}

func TestCompletionKeywordsAndIdentifiers(t *testing.T) {
	h := newHarness(t)
	st := h.open("/w/c.go", "package p\n\nfunc counterTotal() {}\n\nfunc g() {\n\tcou\n\tfu\n}\n")
	got := h.s.completion(st.uri, lsp.Position{Line: 5, Character: 4})
	labels := map[string]bool{}
	for _, it := range got.Items {
		labels[it.Label] = true
		if !strings.HasPrefix(it.Label, "cou") {
			t.Errorf("label %q does not match the typed prefix", it.Label)
		}
	}
	if !labels["counterTotal"] {
		t.Errorf("counterTotal missing: %v", labels)
	}
	got = h.s.completion(st.uri, lsp.Position{Line: 6, Character: 3})
	kw := false
	for _, it := range got.Items {
		if it.Label == "func" && it.Kind == lsp.CompletionItemKindKeyword {
			kw = true
		}
	}
	if !kw {
		t.Errorf("keyword func missing from %v", got.Items)
	}
}

func TestUnknownLanguageAnswersEmpty(t *testing.T) {
	h := newHarness(t)
	st := h.open("/w/data.zzzunknown", "whatever\n")
	if st.lang != nil {
		t.Skip("extension unexpectedly known")
	}
	if got := h.s.documentSymbols(st.uri); got == nil || len(got) != 0 {
		t.Errorf("symbols = %#v", got)
	}
	if got := h.s.foldingRanges(st.uri); got == nil || len(got) != 0 {
		t.Errorf("folds = %#v", got)
	}
	if got := h.s.semanticTokens(st.uri); got.Data == nil || len(got.Data) != 0 {
		t.Errorf("tokens = %#v", got)
	}
	if got := h.s.completion(st.uri, lsp.Position{}); got.Items == nil {
		t.Errorf("completion items nil")
	}
}

func TestOverrideQueriesCompile(t *testing.T) {
	for _, name := range OverrideLanguages() {
		l := LangByName(name)
		if l == nil {
			t.Errorf("override for unregistered language %q", name)
			continue
		}
		f := l.Features()
		if f.TagsSource != "override" || f.TagsError != "" || !f.Xref {
			t.Errorf("%s: %+v", name, f)
		}
	}
}

func TestCaptureMapping(t *testing.T) {
	cases := map[string]string{
		"keyword": "keyword", "keyword.control.return": "keyword", "string": "string", "string.escape": "string",
		"string.regex": "regexp", "comment": "comment", "comment.documentation": "comment", "number": "number",
		"function": "function", "function.method": "method", "function.builtin": "function", "type": "type",
		"type.builtin": "type", "variable": "variable", "variable.parameter": "parameter", "property": "property",
		"operator": "operator", "attribute": "decorator", "constant.builtin": "variable", "module": "namespace",
		"markup.heading.1": "type", "function.macro": "macro",
	}
	for cap, want := range cases {
		typ, _, ok := captureType(cap)
		if !ok || TokenTypes[typ] != want {
			t.Errorf("%s -> %v ok=%v, want %s", cap, TokenTypes[typ], ok, want)
		}
	}
	for _, cap := range []string{"punctuation.bracket", "spell", "nospell", "conceal", "none", "error"} {
		if _, _, ok := captureType(cap); ok {
			t.Errorf("%s should be dropped", cap)
		}
	}
	if _, m, _ := captureType("function.builtin"); m&modDefaultLibrary == 0 {
		t.Error("builtin lost its defaultLibrary modifier")
	}
}
