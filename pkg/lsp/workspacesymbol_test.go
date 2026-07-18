package lsp

import "testing"

func TestWorkspaceSymbol(t *testing.T) {
	const body = `[
	  {"name":"Top","kind":3,"location":{"uri":"file:///a.org","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}}}},
	  {"name":"Sub","kind":15,"location":{"uri":"file:///a.org","range":{"start":{"line":5,"character":0},"end":{"line":5,"character":0}}},"containerName":"Top"}
	]`
	client := newClientCanned(t, map[string]string{workspaceSymbolMethod: body})
	syms, err := client.WorkspaceSymbol(testCtx(t), WorkspaceSymbolParams{Query: "top"})
	if err != nil {
		t.Fatalf("WorkspaceSymbol: %v", err)
	}
	if len(syms) != 2 || syms[0].Name != "Top" || syms[1].ContainerName != "Top" {
		t.Fatalf("syms = %+v", syms)
	}
}

func TestWorkspaceSymbolEmptyQuery(t *testing.T) {
	client := newClientCanned(t, map[string]string{workspaceSymbolMethod: `[]`})
	syms, err := client.WorkspaceSymbol(testCtx(t), WorkspaceSymbolParams{Query: ""})
	if err != nil {
		t.Fatalf("WorkspaceSymbol: %v", err)
	}
	if len(syms) != 0 {
		t.Fatalf("syms = %+v, want empty", syms)
	}
}

func TestWorkspaceSymbolNull(t *testing.T) {
	client := newClientCanned(t, map[string]string{workspaceSymbolMethod: `null`})
	syms, err := client.WorkspaceSymbol(testCtx(t), WorkspaceSymbolParams{})
	if err != nil {
		t.Fatalf("WorkspaceSymbol: %v", err)
	}
	if syms != nil {
		t.Fatalf("syms = %+v, want nil", syms)
	}
}

func TestWorkspaceSymbolMalformed(t *testing.T) {
	c := newClientCanned(t, map[string]string{workspaceSymbolMethod: `42`})
	if _, err := c.WorkspaceSymbol(testCtx(t), WorkspaceSymbolParams{}); err == nil {
		t.Error("WorkspaceSymbol(42) = nil error, want error")
	}
}
