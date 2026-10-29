package gotmpl

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidwalter0/gotreesitter"
	"github.com/davidwalter0/gotreesitter/grammars"
)

func mustLang(t *testing.T) *gotreesitter.Language {
	t.Helper()
	l, err := Language()
	if err != nil {
		t.Fatalf("Language: %v", err)
	}
	return l
}

func parse(t *testing.T, src []byte) *gotreesitter.Tree {
	t.Helper()
	tree, err := gotreesitter.NewParser(mustLang(t)).Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return tree
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// errorLines collects the 1-based lines of ERROR and MISSING nodes.
func errorLines(tree *gotreesitter.Tree) []int {
	var lines []int
	var walk func(n *gotreesitter.Node)
	walk = func(n *gotreesitter.Node) {
		if n.IsError() || n.IsMissing() {
			lines = append(lines, int(n.StartPoint().Row)+1)
		}
		for i := 0; i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(tree.RootNode())
	return lines
}

func TestCleanFixturesHaveNoErrors(t *testing.T) {
	for _, name := range []string{"chart/templates/deployment.yaml", "chart/templates/_helpers.tpl"} {
		tree := parse(t, fixture(t, name))
		if lines := errorLines(tree); len(lines) != 0 || tree.RootNode().HasError() {
			t.Errorf("%s: error/missing nodes at lines %v", name, lines)
		}
	}
	for _, src := range []string{
		"plain: yaml\n",
		"{{ .Values.a }}\n",
		"{{- define \"x\" -}}a{{- end }}\n",
		"{{ range $i, $e := .Values.l }}{{ $e }}{{ end }}\n",
		"{{ with .A }}{{ . }}{{ else }}none{{ end }}\n",
		"{{ \"}}\" }}\n",
	} {
		if tree := parse(t, []byte(src)); tree.RootNode().HasError() {
			t.Errorf("%q: unexpected error nodes at lines %v", src, errorLines(tree))
		}
	}
}

func TestBrokenActionsReportAtTheRightLine(t *testing.T) {
	// broken_unclosed.yaml: `{{ if .Values.x }` is on line 6.
	// broken_noend.yaml: `{{- if ... }}` on line 5 is never ended; the
	// missing {{ end }} surfaces at the end of the document.
	cases := []struct {
		file     string
		min, max int
	}{
		{"broken_unclosed.yaml", 6, 6},
		{"broken_noend.yaml", 5, 7},
	}
	for _, c := range cases {
		tree := parse(t, fixture(t, c.file))
		if !tree.RootNode().HasError() {
			t.Errorf("%s: expected error nodes, tree is clean: %s", c.file, tree.RootNode().SExpr(mustLang(t)))
			continue
		}
		lines := errorLines(tree)
		if len(lines) == 0 {
			t.Errorf("%s: HasError but no ERROR/MISSING node found", c.file)
			continue
		}
		ok := false
		for _, l := range lines {
			if l >= c.min && l <= c.max {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s: error lines %v, want one in [%d,%d]", c.file, lines, c.min, c.max)
		}
	}
}

func TestHighlightQueryCompilesAndCaptures(t *testing.T) {
	l := mustLang(t)
	q, err := gotreesitter.NewQuery(HighlightQuery(), l)
	if err != nil {
		t.Fatalf("NewQuery: %v", err)
	}
	src := fixture(t, "chart/templates/deployment.yaml")
	tree := parse(t, src)
	got := map[string]int{}
	names := q.CaptureNames()
	for _, m := range q.Execute(tree) {
		for _, c := range m.Captures {
			got[c.Name]++
		}
	}
	_ = names
	for _, want := range []string{"keyword", "punctuation.bracket", "property", "operator", "string"} {
		if got[want] == 0 {
			t.Errorf("no @%s captures; got %v", want, got)
		}
	}
}

func TestRegisterIsIdempotent(t *testing.T) {
	before := len(grammars.AllLanguages())
	Register()
	Register()
	after := len(grammars.AllLanguages())
	if after-before > 1 {
		t.Errorf("Register added %d entries", after-before)
	}
	e := grammars.DetectLanguageByName(Name)
	if e == nil {
		t.Fatal("gotmpl not found by name")
	}
	if e.HighlightQuery == "" {
		t.Error("registered without a highlight query")
	}
	if d := grammars.DetectLanguage("templates/_helpers.tpl"); d == nil || d.Name != Name {
		t.Errorf("DetectLanguage(.tpl) = %v", d)
	}
	Register()
	if n := len(grammars.AllLanguages()); n != after {
		t.Errorf("third Register changed entry count %d -> %d", after, n)
	}
}

func TestBlankActions(t *testing.T) {
	src := fixture(t, "chart/templates/deployment.yaml")
	out := BlankActions(src)
	if len(out) != len(src) {
		t.Fatalf("length %d != %d", len(out), len(src))
	}
	if bytes.Contains(out, []byte("{{")) || bytes.Contains(out, []byte("}}")) {
		t.Error("an action survived")
	}
	if bytes.Count(out, []byte{'\n'}) != bytes.Count(src, []byte{'\n'}) {
		t.Error("newline count changed")
	}
	for i := range src {
		if src[i] == '\n' && out[i] != '\n' {
			t.Fatalf("newline at %d moved", i)
		}
	}
	if !bytes.Contains(out, []byte("kind: Deployment")) || !bytes.Contains(out, []byte("          ports:\n")) {
		t.Error("raw text damaged")
	}
	sp := func(n int) string { return strings.Repeat(" ", n) }
	for _, tc := range []struct{ in, want string }{
		{"a: {{ \"}}\" }}b", "a: " + sp(len("{{ \"}}\" }}")) + "b"},
		{"a: {{ `}}` }}b", "a: " + sp(len("{{ `}}` }}")) + "b"},
		{"{{ x\n y }}z", sp(len("{{ x")) + "\n" + sp(len(" y }}")) + "z"},
		{"k: {{ if .x }\nz", "k: " + sp(len("{{ if .x }")) + "\nz"}, // unclosed: blanked to end of line
		{"{{/* }} */}}k", sp(len("{{/* }} */}}")) + "k"},
	} {
		if got := string(BlankActions([]byte(tc.in))); got != tc.want {
			t.Errorf("BlankActions(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	in := []byte("x: {{ y }}")
	if got := BlankActions(in); string(got) != "x: "+sp(len("{{ y }}")) || string(in) != "x: {{ y }}" {
		t.Errorf("input mutated or wrong output: %q / %q", got, in)
	}
}

func TestYAMLRanges(t *testing.T) {
	src := fixture(t, "chart/templates/deployment.yaml")
	tree := parse(t, src)
	rs := YAMLRanges(tree, src)
	if len(rs) < 10 {
		t.Fatalf("only %d ranges", len(rs))
	}
	var joined strings.Builder
	prev := uint32(0)
	for _, r := range rs {
		if r.StartByte < prev || r.EndByte <= r.StartByte {
			t.Fatalf("ranges not ordered/non-empty: %+v", r)
		}
		prev = r.EndByte
		joined.Write(src[r.StartByte:r.EndByte])
		if r.StartPoint != pointAt(src, int(r.StartByte)) || r.EndPoint != pointAt(src, int(r.EndByte)) {
			t.Fatalf("bad points %+v", r)
		}
	}
	if strings.Contains(joined.String(), "{{") || strings.Contains(joined.String(), "}}") {
		t.Error("an action leaked into YAML ranges")
	}
	// The ranges are exactly the complement of the lexical action spans.
	in := make([]bool, len(src))
	for _, r := range rs {
		for i := r.StartByte; i < r.EndByte; i++ {
			in[i] = true
		}
	}
	for i := range src {
		if in[i] == isActionByte(src, i) {
			t.Fatalf("byte %d (%q): range membership %v disagrees with the lexical action span", i, src[i], in[i])
		}
	}
	for _, want := range []string{"kind: Deployment", "imagePullSecrets:", "containerPort: "} {
		if !strings.Contains(joined.String(), want) {
			t.Errorf("missing %q in YAML text", want)
		}
	}
	// Whole file when there are no actions; nothing for empty input.
	plain := []byte("a: 1\n")
	if r := YAMLRanges(parse(t, plain), plain); len(r) != 1 || r[0].EndByte != uint32(len(plain)) {
		t.Errorf("plain: %+v", r)
	}
	if YAMLRanges(nil, nil) != nil {
		t.Error("empty input should give nil")
	}
	// Broken action: only the rest of its line is excluded.
	br := fixture(t, "broken_unclosed.yaml")
	for _, r := range YAMLRanges(parse(t, br), br) {
		if strings.Contains(string(br[r.StartByte:r.EndByte]), "{{ if") {
			t.Error("unclosed action leaked into YAML ranges")
		}
	}
	// And the ranges drive the parser's included-ranges mode.
	p := gotreesitter.NewParser(mustLang(t))
	p.SetIncludedRanges(rs)
	if _, err := p.Parse(src); err != nil {
		t.Errorf("parse with included ranges: %v", err)
	}
}

// isActionByte reports whether byte i lies inside a lexically found action.
func isActionByte(src []byte, i int) bool {
	for _, s := range actionSpans(src) {
		if i >= s[0] && i < s[1] {
			return true
		}
	}
	return false
}

func TestIsHelmTemplate(t *testing.T) {
	root := t.TempDir()
	w := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	w("chart/Chart.yaml")
	for _, rel := range []string{
		"chart/templates/deployment.yaml", "chart/templates/_helpers.tpl",
		"chart/templates/x.yml", "chart/templates/tests/t.yaml",
		"chart/charts/sub/templates/a.yaml",
	} {
		if p := w(rel); !IsHelmTemplate(p) {
			t.Errorf("%s should be a helm template", rel)
		}
	}
	w("chart/charts/sub/Chart.yaml")
	for _, rel := range []string{
		"chart/values.yaml", "chart/templates/notes.txt", "chart/templates/x.json",
		"plain/templates/deployment.yaml", "chart/other/deployment.yaml",
	} {
		if p := w(rel); IsHelmTemplate(p) {
			t.Errorf("%s should not be a helm template", rel)
		}
	}
	if IsHelmTemplate("") || IsHelmTemplate("templates/x.yaml") {
		t.Error("relative path without a chart matched")
	}
}

// TestBlobMatchesGenerator regenerates the blob from grammar.json and
// requires it to equal the committed one: a stale blob still loads and
// silently misparses, so drift must fail loudly. It takes several seconds.
func TestBlobMatchesGenerator(t *testing.T) {
	if testing.Short() {
		t.Skip("regenerates the grammar (seconds)")
	}
	out := filepath.Join(t.TempDir(), "gotmpl.bin")
	cmd := exec.Command("go", "run", "./internal/gen", "-json", "grammar.json", "-bin", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, b)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, blob) {
		t.Fatalf("gotmpl.bin is stale: run `go generate ./grammars/gotmpl` (regenerated %d bytes, committed %d)", len(got), len(blob))
	}
}
