// Package e2e drives the real gts-lsp binary over stdio.
package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/gtslsp/gts"
	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/lspbridge/pkg/lspserver"
)

var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gts-lsp-e2e")
	if err != nil {
		panic(err)
	}
	binPath = filepath.Join(dir, "gts-lsp")
	cmd := exec.Command("go", "build", "-o", binPath, "github.com/davidwalter0/lspbridge/gtslsp/cmd/gts-lsp")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		panic("building gts-lsp: " + err.Error())
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// session is one gts-lsp process plus a jsonrpc client.
type session struct {
	t     *testing.T
	cmd   *exec.Cmd
	conn  *jsonrpc.Conn
	mu    sync.Mutex
	diags map[lsp.DocumentURI]chan []lsp.Diagnostic
	msgs  chan string
	caps  map[string]json.RawMessage
	info  *lsp.ServerInfo
}

func start(t *testing.T) *session {
	t.Helper()
	cmd := exec.Command(binPath)
	cmd.Stderr = os.Stderr
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &session{t: t, cmd: cmd, diags: map[lsp.DocumentURI]chan []lsp.Diagnostic{}, msgs: make(chan string, 16)}
	s.conn = jsonrpc.NewConn(jsonrpc.Join(out, in), jsonrpc.HandlerFunc(s.onNotify))
	t.Cleanup(func() {
		if s.cmd.ProcessState == nil {
			_ = s.cmd.Process.Kill()
			_ = s.cmd.Wait()
		}
	})
	var res lsp.InitializeResult
	s.call("initialize", lsp.InitializeParams{ProcessID: os.Getpid(), RootURI: "file:///"}, &res)
	if err := json.Unmarshal(res.Capabilities, &s.caps); err != nil {
		t.Fatal(err)
	}
	s.info = res.ServerInfo
	_ = s.conn.Notify(context.Background(), "initialized", struct{}{})
	return s
}

func (s *session) onNotify(_ context.Context, r *jsonrpc.Request) (any, error) {
	switch r.Method {
	case "textDocument/publishDiagnostics":
		var p lsp.PublishDiagnosticsParams
		if json.Unmarshal(r.Params, &p) == nil {
			s.diagChan(p.URI) <- p.Diagnostics
		}
	case "window/showMessage":
		var p lsp.ShowMessageParams
		if json.Unmarshal(r.Params, &p) == nil {
			s.msgs <- p.Message
		}
	}
	return nil, nil
}

func (s *session) diagChan(uri lsp.DocumentURI) chan []lsp.Diagnostic {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.diags[uri]
	if c == nil {
		c = make(chan []lsp.Diagnostic, 64)
		s.diags[uri] = c
	}
	return c
}

func (s *session) nextDiags(uri lsp.DocumentURI) []lsp.Diagnostic {
	s.t.Helper()
	select {
	case d := <-s.diagChan(uri):
		return d
	case <-time.After(20 * time.Second):
		s.t.Fatalf("no publishDiagnostics for %s", uri)
		return nil
	}
}

func (s *session) call(method string, params, result any) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := s.conn.Call(ctx, method, params, result); err != nil {
		s.t.Fatalf("%s: %v", method, err)
	}
}

func (s *session) open(path string) (lsp.DocumentURI, string) {
	s.t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		s.t.Fatal(err)
	}
	abs, _ := filepath.Abs(path)
	uri := lspserver.PathToURI(abs)
	_ = s.conn.Notify(context.Background(), "textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "x", Version: 1, Text: string(src)}})
	return uri, string(src)
}

func (s *session) shutdown() int {
	s.t.Helper()
	s.call("shutdown", nil, nil)
	_ = s.conn.Notify(context.Background(), "exit", nil)
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return ee.ExitCode()
			}
			s.t.Fatal(err)
		}
		return 0
	case <-time.After(10 * time.Second):
		s.t.Fatal("gts-lsp did not exit after exit notification")
		return -1
	}
}

func flat(syms []lsp.DocumentSymbol, prefix string) []string {
	var out []string
	for _, y := range syms {
		out = append(out, prefix+y.Name)
		out = append(out, flat(y.Children, prefix+y.Name+"/")...)
	}
	return out
}

type langCase struct {
	dir string
	// symbols that must appear (as "outer/inner" paths) in the clean fixture
	symbols []string
	// folds expected on the clean fixture (at least)
	minFolds int
	// markdown has no syntax errors
	noBroken bool
}

var cases = []langCase{
	{"c", []string{"add", "main", "node", "point_t", "point_t/point", "MAX_ITEMS", "counter"}, 4, false},
	{"cpp", []string{"shapes", "shapes/Shape", "shapes/Shape/area", "shapes/Circle", "shapes/twice", "total"}, 5, false},
	{"rust", []string{"Point", "Shape", "Area", "Area/area", "util", "util/double", "origin"}, 5, false},
	{"go", []string{"Greeter", "Speaker", "Version", "counter", "Speak", "NewGreeter", "main"}, 4, false},
	{"bash", []string{"say", "build", "COUNT", "greeting"}, 2, false},
	{"yaml", []string{"name", "image", "image/repository", "ports", "env", "env/LOG_LEVEL"}, 3, false},
	{"json", []string{"name", "scripts", "scripts/build", "tags"}, 1, false},
	{"toml", []string{"title", "package", "dependencies", "bin"}, 2, false},
	{"dockerfile", []string{"build"}, 0, false},
	{"make", []string{"CC", "all", "app", "clean"}, 3, false},
	{"markdown", []string{"Title", "Title/Section One", "Title/Section Two", "Title/Section Two/Nested"}, 3, true},
}

func fixture(dir, kind string) string {
	ents, err := os.ReadDir(filepath.Join("..", "testdata", dir, kind))
	if err != nil || len(ents) != 1 {
		panic("fixture " + dir + "/" + kind)
	}
	return filepath.Join("..", "testdata", dir, kind, ents[0].Name())
}

func TestInitializeAdvertisesTheUnionAndLabelsHeuristics(t *testing.T) {
	s := start(t)
	for _, k := range []string{"documentSymbolProvider", "foldingRangeProvider", "selectionRangeProvider",
		"semanticTokensProvider", "definitionProvider", "referencesProvider", "completionProvider", "textDocumentSync"} {
		if _, ok := s.caps[k]; !ok {
			t.Errorf("capability %s missing: %v", k, keys(s.caps))
		}
	}
	for _, k := range []string{"hoverProvider", "renameProvider", "diagnosticProvider"} {
		if _, ok := s.caps[k]; ok {
			t.Errorf("must not advertise %s", k)
		}
	}
	if string(s.caps["positionEncoding"]) != `"utf-16"` {
		t.Errorf("positionEncoding = %s", s.caps["positionEncoding"])
	}
	if s.info == nil || s.info.Name != "gts-lsp" || !strings.Contains(s.info.Version, "HEURISTIC") {
		t.Errorf("serverInfo = %+v", s.info)
	}
	var sem struct {
		Legend lsp.SemanticTokensLegend `json:"legend"`
		Full   bool                     `json:"full"`
	}
	if err := json.Unmarshal(s.caps["semanticTokensProvider"], &sem); err != nil || !sem.Full || len(sem.Legend.TokenTypes) == 0 {
		t.Errorf("semanticTokensProvider = %s", s.caps["semanticTokensProvider"])
	}
	if code := s.shutdown(); code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
}

func keys(m map[string]json.RawMessage) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

func TestLanguages(t *testing.T) {
	s := start(t)
	var legend lsp.SemanticTokensLegend
	{
		var sem struct {
			Legend lsp.SemanticTokensLegend `json:"legend"`
		}
		_ = json.Unmarshal(s.caps["semanticTokensProvider"], &sem)
		legend = sem.Legend
	}
	for _, c := range cases {
		t.Run(c.dir, func(t *testing.T) {
			s.t = t
			// ---- broken: diagnostics ----
			buri, _ := s.open(fixture(c.dir, "broken"))
			bd := s.nextDiags(buri)
			if c.noBroken {
				if len(bd) != 0 {
					t.Errorf("markdown cannot have syntax errors, got %+v", bd)
				}
			} else {
				if len(bd) == 0 {
					t.Errorf("broken fixture produced no diagnostics")
				}
				for _, d := range bd {
					if d.Source != "gts" || d.Severity != lsp.SeverityError || d.Message == "" {
						t.Errorf("bad diagnostic %+v", d)
					}
				}
			}
			// ---- clean ----
			uri, text := s.open(fixture(c.dir, "clean"))
			if d := s.nextDiags(uri); len(d) != 0 {
				t.Errorf("clean fixture has diagnostics: %+v", d)
			}
			var syms []lsp.DocumentSymbol
			s.call("textDocument/documentSymbol", lsp.DocumentSymbolParams{TextDocument: lsp.TextDocumentIdentifier{URI: uri}}, &syms)
			got := map[string]bool{}
			for _, n := range flat(syms, "") {
				got[n] = true
			}
			for _, want := range c.symbols {
				if !got[want] {
					t.Errorf("symbol %q missing; have %v", want, flat(syms, ""))
				}
			}
			var folds []lsp.FoldingRange
			s.call("textDocument/foldingRange", lsp.FoldingRangeParams{TextDocument: lsp.TextDocumentIdentifier{URI: uri}}, &folds)
			if len(folds) < c.minFolds {
				t.Errorf("%d folds, want >= %d", len(folds), c.minFolds)
			}
			startSeen := map[int]bool{}
			for _, f := range folds {
				if startSeen[f.StartLine] || f.EndLine <= f.StartLine {
					t.Errorf("bad fold %+v", f)
				}
				startSeen[f.StartLine] = true
			}
			checkTokens(t, s, uri, text, legend)
		})
	}
	s.t = t
	if code := s.shutdown(); code != 0 {
		t.Errorf("exit code %d", code)
	}
}

// checkTokens decodes semanticTokens/full and checks each token against the
// document: ordered, non-overlapping, inside its line (UTF-16), valid type.
func checkTokens(t *testing.T, s *session, uri lsp.DocumentURI, text string, legend lsp.SemanticTokensLegend) {
	t.Helper()
	var tok lsp.SemanticTokens
	s.call("textDocument/semanticTokens/full", lsp.SemanticTokensParams{TextDocument: lsp.TextDocumentIdentifier{URI: uri}}, &tok)
	if len(tok.Data) == 0 || len(tok.Data)%5 != 0 {
		t.Errorf("semantic token data length %d", len(tok.Data))
		return
	}
	lines := strings.Split(text, "\n")
	prevLine, prevEnd := 0, 0
	for i, d := range gts.DecodeTokens(tok.Data) {
		line, char, length, typ := d[0], d[1], d[2], d[3]
		if line >= len(lines) || typ >= len(legend.TokenTypes) {
			t.Errorf("token %d out of range: %v", i, d)
			return
		}
		units := utf16Len(strings.TrimRight(lines[line], "\r"))
		if length <= 0 || char < 0 || char+length > units {
			t.Errorf("token %d %v exceeds line %d (%d units): %q", i, d, line, units, lines[line])
			return
		}
		if i > 0 && (line < prevLine || line == prevLine && char < prevEnd) {
			t.Errorf("token %d %v overlaps or precedes the previous one", i, d)
			return
		}
		prevLine, prevEnd = line, char+length
	}
	// Re-encode what we decoded: delta encoding must round-trip.
	dec := gts.DecodeTokens(tok.Data)
	var re []int
	pl, pc := 0, 0
	for _, d := range dec {
		dl := d[0] - pl
		dc := d[1]
		if dl == 0 {
			dc = d[1] - pc
		}
		re = append(re, dl, dc, d[2], d[3], d[4])
		pl, pc = d[0], d[1]
	}
	if len(re) != len(tok.Data) {
		t.Fatalf("round trip length %d != %d", len(re), len(tok.Data))
	}
	for i := range re {
		if re[i] != tok.Data[i] {
			t.Fatalf("round trip differs at %d", i)
		}
	}
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func TestIncrementalEditOverTheWire(t *testing.T) {
	s := start(t)
	uri := lsp.DocumentURI("file:///w/sample.go")
	_ = s.conn.Notify(context.Background(), "textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "go", Version: 1,
			Text: "package p\n\nvar s = \"😀\"\n\nfunc f() {}\n"}})
	if d := s.nextDiags(uri); len(d) != 0 {
		t.Fatalf("initial diagnostics %+v", d)
	}
	// Break it: replace the 2-unit emoji + quote region, positions in UTF-16.
	// line 2: var s = "😀"   (😀 at units 9..11, closing quote at 11)
	edit := func(version int, r lsp.Range, text string) {
		_ = s.conn.Notify(context.Background(), "textDocument/didChange", lsp.DidChangeTextDocumentParams{
			TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri, Version: version},
			ContentChanges: []lsp.TextDocumentContentChangeEvent{{Range: &r, Text: text}},
		})
	}
	rng := func(l1, c1, l2, c2 int) lsp.Range {
		return lsp.Range{Start: lsp.Position{Line: l1, Character: c1}, End: lsp.Position{Line: l2, Character: c2}}
	}
	edit(2, rng(2, 11, 2, 12), "") // delete the closing quote: unterminated string
	if d := s.nextDiags(uri); len(d) == 0 {
		t.Fatal("unterminated string: expected diagnostics")
	}
	edit(3, rng(2, 11, 2, 11), "\"") // put it back (after the emoji, utf-16 col 11)
	if d := s.nextDiags(uri); len(d) != 0 {
		t.Fatalf("repaired file still has diagnostics: %+v", d)
	}
	// A symbol added after the emoji line is found on the right line.
	edit(4, rng(4, 0, 4, 0), "func added() {}\n")
	_ = s.nextDiags(uri)
	var syms []lsp.DocumentSymbol
	s.call("textDocument/documentSymbol", lsp.DocumentSymbolParams{TextDocument: lsp.TextDocumentIdentifier{URI: uri}}, &syms)
	found := false
	for _, y := range syms {
		if y.Name == "added" && y.SelectionRange.Start.Line == 4 && y.SelectionRange.Start.Character == 5 {
			found = true
		}
	}
	if !found {
		t.Errorf("added() not at 4:5 in %v", flat(syms, ""))
	}
	// Close clears diagnostics.
	_ = s.conn.Notify(context.Background(), "textDocument/didClose", lsp.DidCloseTextDocumentParams{TextDocument: lsp.TextDocumentIdentifier{URI: uri}})
	if d := s.nextDiags(uri); len(d) != 0 {
		t.Errorf("close should clear diagnostics, got %+v", d)
	}
	if code := s.shutdown(); code != 0 {
		t.Errorf("exit code %d", code)
	}
}

func TestPreInitializeRequestAndExitCodes(t *testing.T) {
	cmd := exec.Command(binPath)
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	conn := jsonrpc.NewConn(jsonrpc.Join(out, in), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := conn.Call(ctx, "textDocument/documentSymbol", lsp.DocumentSymbolParams{}, nil)
	var rpcErr *jsonrpc.Error
	if !asRPC(err, &rpcErr) || rpcErr.Code != jsonrpc.CodeServerNotInitialized {
		t.Errorf("pre-initialize error = %v, want ServerNotInitialized", err)
	}
	// exit without shutdown: code 1.
	_ = conn.Notify(ctx, "exit", nil)
	werr := cmd.Wait()
	if ee, ok := werr.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Errorf("exit without shutdown: %v, want exit status 1", werr)
	}
}

func asRPC(err error, target **jsonrpc.Error) bool {
	e, ok := err.(*jsonrpc.Error)
	if ok {
		*target = e
	}
	return ok
}

func TestCapabilitiesFlag(t *testing.T) {
	out, err := exec.Command(binPath, "--capabilities").Output()
	if err != nil {
		t.Fatal(err)
	}
	var rows []gts.Capability
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) < 100 {
		t.Fatalf("only %d languages", len(rows))
	}
	by := map[string]gts.Features{}
	for _, r := range rows {
		by[r.Language] = r.Features
	}
	for _, c := range cases {
		f, ok := by[c.dir]
		if !ok {
			t.Errorf("%s missing from --capabilities", c.dir)
			continue
		}
		if !f.Diagnostics || !f.Symbols || !f.SemanticTokens || !f.Xref {
			t.Errorf("%s: %+v", c.dir, f)
		}
		if f.TagsSource != "override" {
			t.Errorf("%s: tagsSource %q, want override", c.dir, f.TagsSource)
		}
	}
}
