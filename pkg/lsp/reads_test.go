package lsp

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
)

// rawServer answers initialize and returns a caller-supplied raw JSON body for
// every other method, letting a test drive the exact wire shape a real server
// would send (including the polymorphic hover/definition forms).
type rawServer struct {
	byMethod map[string]string
}

func (s rawServer) Handle(_ context.Context, req *jsonrpc.Request) (any, error) {
	if req.Method == "initialize" {
		return InitializeResult{Capabilities: json.RawMessage(`{}`), ServerInfo: &ServerInfo{Name: "fake"}}, nil
	}
	if body, ok := s.byMethod[req.Method]; ok {
		return json.RawMessage(body), nil
	}
	return nil, nil
}

func newClientCanned(t *testing.T, byMethod map[string]string) *Client {
	t.Helper()
	a, b := net.Pipe()
	serverConn := jsonrpc.NewConn(b, rawServer{byMethod: byMethod})
	clientConn := jsonrpc.NewConn(a, nil)
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return NewClient(clientConn)
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestDocumentSymbolHierarchical(t *testing.T) {
	const body = `[
	  {"name":"C","kind":5,"range":{"start":{"line":0,"character":0},"end":{"line":9,"character":0}},
	   "selectionRange":{"start":{"line":0,"character":6},"end":{"line":0,"character":7}},
	   "children":[
	     {"name":"m","kind":6,"range":{"start":{"line":1,"character":2},"end":{"line":2,"character":0}},
	      "selectionRange":{"start":{"line":1,"character":6},"end":{"line":1,"character":7}}}
	   ]}
	]`
	client := newClientCanned(t, map[string]string{documentSymbolMethod: body})
	res, err := client.DocumentSymbol(testCtx(t), DocumentSymbolParams{TextDocument: TextDocumentIdentifier{URI: "file:///a.py"}})
	if err != nil {
		t.Fatalf("DocumentSymbol: %v", err)
	}
	if res.Flat != nil {
		t.Fatalf("expected hierarchical result, got flat %+v", res.Flat)
	}
	if len(res.Hierarchical) != 1 || res.Hierarchical[0].Name != "C" || res.Hierarchical[0].Kind != SymbolKindClass {
		t.Fatalf("hierarchical = %+v", res.Hierarchical)
	}
	if len(res.Hierarchical[0].Children) != 1 || res.Hierarchical[0].Children[0].Name != "m" {
		t.Fatalf("children = %+v", res.Hierarchical[0].Children)
	}
}

func TestDocumentSymbolFlat(t *testing.T) {
	const body = `[
	  {"name":"f","kind":12,"location":{"uri":"file:///a.py","range":{"start":{"line":3,"character":0},"end":{"line":3,"character":5}}},"containerName":"mod"}
	]`
	client := newClientCanned(t, map[string]string{documentSymbolMethod: body})
	res, err := client.DocumentSymbol(testCtx(t), DocumentSymbolParams{})
	if err != nil {
		t.Fatalf("DocumentSymbol: %v", err)
	}
	if res.Hierarchical != nil {
		t.Fatalf("expected flat result, got hierarchical %+v", res.Hierarchical)
	}
	if len(res.Flat) != 1 || res.Flat[0].Name != "f" || res.Flat[0].ContainerName != "mod" {
		t.Fatalf("flat = %+v", res.Flat)
	}
	if res.Flat[0].Location.URI != "file:///a.py" {
		t.Fatalf("location = %+v", res.Flat[0].Location)
	}
}

func TestDocumentSymbolEmptyAndNull(t *testing.T) {
	for name, body := range map[string]string{"empty-array": `[]`, "null": `null`} {
		t.Run(name, func(t *testing.T) {
			client := newClientCanned(t, map[string]string{documentSymbolMethod: body})
			res, err := client.DocumentSymbol(testCtx(t), DocumentSymbolParams{})
			if err != nil {
				t.Fatalf("DocumentSymbol: %v", err)
			}
			if len(res.Hierarchical) != 0 || len(res.Flat) != 0 {
				t.Fatalf("expected empty result, got %+v", res)
			}
		})
	}
}

func TestReferences(t *testing.T) {
	const body = `[
	  {"uri":"file:///a.py","range":{"start":{"line":0,"character":4},"end":{"line":0,"character":7}}},
	  {"uri":"file:///a.py","range":{"start":{"line":5,"character":0},"end":{"line":5,"character":3}}}
	]`
	client := newClientCanned(t, map[string]string{referencesMethod: body})
	locs, err := client.References(testCtx(t), ReferenceParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///a.py"},
		Position:     Position{Line: 0, Character: 4},
		Context:      ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if len(locs) != 2 {
		t.Fatalf("want 2 locations, got %d: %+v", len(locs), locs)
	}
}

func TestReferencesNull(t *testing.T) {
	client := newClientCanned(t, map[string]string{referencesMethod: `null`})
	locs, err := client.References(testCtx(t), ReferenceParams{})
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if locs != nil {
		t.Fatalf("want nil, got %+v", locs)
	}
}

func TestDefinition(t *testing.T) {
	cases := map[string]struct {
		body string
		want int
	}{
		"single-object": {`{"uri":"file:///a.py","range":{"start":{"line":1,"character":0},"end":{"line":1,"character":3}}}`, 1},
		"array":         {`[{"uri":"file:///a.py","range":{"start":{"line":1,"character":0},"end":{"line":1,"character":3}}}]`, 1},
		"null":          {`null`, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			client := newClientCanned(t, map[string]string{definitionMethod: tc.body})
			locs, err := client.Definition(testCtx(t), DefinitionParams{})
			if err != nil {
				t.Fatalf("Definition: %v", err)
			}
			if len(locs) != tc.want {
				t.Fatalf("want %d locations, got %d: %+v", tc.want, len(locs), locs)
			}
		})
	}
}

func TestHoverForms(t *testing.T) {
	cases := map[string]struct {
		body      string
		wantKind  string
		wantValue string
	}{
		"markupcontent":     {`{"contents":{"kind":"markdown","value":"# doc"}}`, "markdown", "# doc"},
		"plain-string":      {`{"contents":"hello"}`, "plaintext", "hello"},
		"markedstring":      {`{"contents":{"language":"python","value":"def f()"}}`, "markdown", "def f()"},
		"array-of-marked":   {`{"contents":[{"language":"python","value":"def f()"},"more"]}`, "markdown", "def f()\n\nmore"},
		"with-range":        {`{"contents":{"kind":"plaintext","value":"v"},"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}`, "plaintext", "v"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			client := newClientCanned(t, map[string]string{hoverMethod: tc.body})
			h, err := client.Hover(testCtx(t), HoverParams{})
			if err != nil {
				t.Fatalf("Hover: %v", err)
			}
			if h == nil {
				t.Fatal("want non-nil hover")
			}
			if h.Contents.Kind != tc.wantKind || h.Contents.Value != tc.wantValue {
				t.Fatalf("contents = %+v, want kind=%q value=%q", h.Contents, tc.wantKind, tc.wantValue)
			}
			if name == "with-range" && h.Range == nil {
				t.Fatal("want non-nil range")
			}
		})
	}
}

func TestHoverNull(t *testing.T) {
	client := newClientCanned(t, map[string]string{hoverMethod: `null`})
	h, err := client.Hover(testCtx(t), HoverParams{})
	if err != nil {
		t.Fatalf("Hover: %v", err)
	}
	if h != nil {
		t.Fatalf("want nil hover, got %+v", h)
	}
}

func TestRename(t *testing.T) {
	const body = `{"changes":{"file:///a.py":[
	  {"range":{"start":{"line":0,"character":4},"end":{"line":0,"character":7}},"newText":"bar"},
	  {"range":{"start":{"line":5,"character":0},"end":{"line":5,"character":3}},"newText":"bar"}
	]}}`
	client := newClientCanned(t, map[string]string{renameMethod: body})
	we, err := client.Rename(testCtx(t), RenameParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///a.py"},
		Position:     Position{Line: 0, Character: 4},
		NewName:      "bar",
	})
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if we == nil || len(we.Changes["file:///a.py"]) != 2 {
		t.Fatalf("workspace edit = %+v", we)
	}
	if we.Changes["file:///a.py"][0].NewText != "bar" {
		t.Fatalf("newText = %q", we.Changes["file:///a.py"][0].NewText)
	}
}

func TestRenameNull(t *testing.T) {
	client := newClientCanned(t, map[string]string{renameMethod: `null`})
	we, err := client.Rename(testCtx(t), RenameParams{})
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if we != nil {
		t.Fatalf("want nil, got %+v", we)
	}
}

func TestWorkspaceEditDocumentChanges(t *testing.T) {
	// The documentChanges form pyright returns (version: null), folded into
	// the Changes map keyed by URI.
	const body = `{"documentChanges":[
	  {"textDocument":{"uri":"file:///a.py","version":null},"edits":[
	    {"range":{"start":{"line":0,"character":4},"end":{"line":0,"character":9}},"newText":"welcome"},
	    {"range":{"start":{"line":5,"character":10},"end":{"line":5,"character":15}},"newText":"welcome"}
	  ]}
	]}`
	var we WorkspaceEdit
	if err := json.Unmarshal([]byte(body), &we); err != nil {
		t.Fatalf("unmarshal documentChanges: %v", err)
	}
	edits := we.Changes["file:///a.py"]
	if len(edits) != 2 || edits[0].NewText != "welcome" {
		t.Fatalf("folded edits = %+v", edits)
	}
}

func TestWorkspaceEditChangesForm(t *testing.T) {
	// The legacy changes-map form still decodes through the custom unmarshaler.
	const body = `{"changes":{"file:///a.py":[
	  {"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":3}},"newText":"y"}
	]}}`
	var we WorkspaceEdit
	if err := json.Unmarshal([]byte(body), &we); err != nil {
		t.Fatalf("unmarshal changes: %v", err)
	}
	if len(we.Changes["file:///a.py"]) != 1 {
		t.Fatalf("changes = %+v", we.Changes)
	}
}

func TestWorkspaceEditSkipsResourceOps(t *testing.T) {
	// A create-file resource op (no edits) is skipped; the text edit is kept.
	const body = `{"documentChanges":[
	  {"kind":"create","uri":"file:///new.py"},
	  {"textDocument":{"uri":"file:///a.py","version":3},"edits":[
	    {"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"z"}
	  ]}
	]}`
	var we WorkspaceEdit
	if err := json.Unmarshal([]byte(body), &we); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(we.Changes) != 1 || len(we.Changes["file:///a.py"]) != 1 {
		t.Fatalf("changes = %+v (resource op should be skipped)", we.Changes)
	}
}

func TestReadMethodsMalformed(t *testing.T) {
	// Each body is valid JSON that fails to decode into the method's result
	// type, exercising the error-return branches.
	t.Run("documentSymbol", func(t *testing.T) {
		for _, body := range []string{`42`, `[42]`, `[{"kind":"x"}]`, `[{"location":42}]`} {
			c := newClientCanned(t, map[string]string{documentSymbolMethod: body})
			if _, err := c.DocumentSymbol(testCtx(t), DocumentSymbolParams{}); err == nil {
				t.Errorf("DocumentSymbol(%s) = nil error, want error", body)
			}
		}
	})
	t.Run("references", func(t *testing.T) {
		c := newClientCanned(t, map[string]string{referencesMethod: `42`})
		if _, err := c.References(testCtx(t), ReferenceParams{}); err == nil {
			t.Error("References(42) = nil error, want error")
		}
	})
	t.Run("definition", func(t *testing.T) {
		for _, body := range []string{`[42]`, `{"uri":42}`} {
			c := newClientCanned(t, map[string]string{definitionMethod: body})
			if _, err := c.Definition(testCtx(t), DefinitionParams{}); err == nil {
				t.Errorf("Definition(%s) = nil error, want error", body)
			}
		}
		// A scalar (neither array nor object) decodes to no locations, no error.
		c := newClientCanned(t, map[string]string{definitionMethod: `42`})
		if locs, err := c.Definition(testCtx(t), DefinitionParams{}); err != nil || locs != nil {
			t.Errorf("Definition(42) = (%v, %v), want (nil, nil)", locs, err)
		}
	})
	t.Run("hover", func(t *testing.T) {
		for _, body := range []string{`42`, `{"contents":{"kind":42}}`, `{"contents":[{"kind":42}]}`} {
			c := newClientCanned(t, map[string]string{hoverMethod: body})
			if _, err := c.Hover(testCtx(t), HoverParams{}); err == nil {
				t.Errorf("Hover(%s) = nil error, want error", body)
			}
		}
	})
	t.Run("rename", func(t *testing.T) {
		for _, body := range []string{`42`, `{"documentChanges":[42]}`} {
			c := newClientCanned(t, map[string]string{renameMethod: body})
			if _, err := c.Rename(testCtx(t), RenameParams{}); err == nil {
				t.Errorf("Rename(%s) = nil error, want error", body)
			}
		}
	})
}

func TestJSONHelpers(t *testing.T) {
	if !isJSONNull(json.RawMessage("  null ")) {
		t.Error("isJSONNull should trim and match null")
	}
	if isJSONNull(json.RawMessage(`{}`)) {
		t.Error("isJSONNull should be false for object")
	}
	if !isJSONNull(nil) {
		t.Error("isJSONNull should be true for empty")
	}
	if got := firstToken(json.RawMessage("  \n\t[")); got != '[' {
		t.Errorf("firstToken = %q, want [", got)
	}
	if got := firstToken(json.RawMessage("   ")); got != 0 {
		t.Errorf("firstToken(blank) = %q, want 0", got)
	}
}
