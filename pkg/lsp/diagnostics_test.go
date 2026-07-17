package lsp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
)

func publishNotification(t *testing.T, uri DocumentURI, diags []Diagnostic) *jsonrpc.Request {
	t.Helper()
	params := PublishDiagnosticsParams{URI: uri, Diagnostics: diags}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return &jsonrpc.Request{JSONRPC: jsonrpc.Version, Method: publishDiagnosticsMethod, Params: raw}
}

func TestDiagnosticsCollectorRecordsPublish(t *testing.T) {
	c := NewDiagnosticsCollector()
	uri := DocumentURI("file:///p/a.py")

	if _, seen := c.Diagnostics(uri); seen {
		t.Fatalf("expected no diagnostics before any publish")
	}

	want := []Diagnostic{{Message: "boom", Severity: SeverityError}}
	if _, err := c.Handle(context.Background(), publishNotification(t, uri, want)); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got, seen := c.Diagnostics(uri)
	if !seen {
		t.Fatalf("expected diagnostics to be seen after publish")
	}
	if len(got) != 1 || got[0].Message != "boom" {
		t.Errorf("Diagnostics = %+v", got)
	}
}

func TestDiagnosticsCollectorIgnoresOtherMethods(t *testing.T) {
	c := NewDiagnosticsCollector()
	req := &jsonrpc.Request{JSONRPC: jsonrpc.Version, Method: "window/logMessage", Params: json.RawMessage(`{}`)}
	result, err := c.Handle(context.Background(), req)
	if err != nil || result != nil {
		t.Fatalf("Handle(other method) = (%v, %v), want (nil, nil)", result, err)
	}
	if _, seen := c.Diagnostics("file:///anything"); seen {
		t.Fatalf("unrelated method must not populate diagnostics")
	}
}

func TestDiagnosticsCollectorHandleMalformedParamsIsNoop(t *testing.T) {
	c := NewDiagnosticsCollector()
	req := &jsonrpc.Request{JSONRPC: jsonrpc.Version, Method: publishDiagnosticsMethod, Params: json.RawMessage(`not-json`)}
	if _, err := c.Handle(context.Background(), req); err != nil {
		t.Fatalf("Handle(malformed): %v", err)
	}
}

func TestDiagnosticsCollectorWaitReturnsAlreadyArrived(t *testing.T) {
	c := NewDiagnosticsCollector()
	uri := DocumentURI("file:///p/a.py")
	want := []Diagnostic{{Message: "already here"}}
	if _, err := c.Handle(context.Background(), publishNotification(t, uri, want)); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := c.Wait(ctx, uri)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(got) != 1 || got[0].Message != "already here" {
		t.Errorf("Wait = %+v", got)
	}
}

func TestDiagnosticsCollectorWaitBlocksUntilPublish(t *testing.T) {
	c := NewDiagnosticsCollector()
	uri := DocumentURI("file:///p/a.py")

	done := make(chan struct{})
	go func() {
		time.Sleep(75 * time.Millisecond)
		_, _ = c.Handle(context.Background(), publishNotification(t, uri, []Diagnostic{{Message: "late"}}))
		close(done)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := c.Wait(ctx, uri)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(got) != 1 || got[0].Message != "late" {
		t.Errorf("Wait = %+v", got)
	}
	<-done
}

func TestDiagnosticsCollectorWaitIgnoresEmptyBatchThenReturnsNonEmpty(t *testing.T) {
	c := NewDiagnosticsCollector()
	uri := DocumentURI("file:///p/a.py")

	// Simulate a server publishing an interim empty batch before the real one.
	if _, err := c.Handle(context.Background(), publishNotification(t, uri, nil)); err != nil {
		t.Fatalf("Handle(empty): %v", err)
	}

	go func() {
		time.Sleep(75 * time.Millisecond)
		_, _ = c.Handle(context.Background(), publishNotification(t, uri, []Diagnostic{{Message: "real"}}))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := c.Wait(ctx, uri)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(got) != 1 || got[0].Message != "real" {
		t.Errorf("Wait = %+v", got)
	}
}

func TestDiagnosticsCollectorWaitRespectsContextCancellation(t *testing.T) {
	c := NewDiagnosticsCollector()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.Wait(ctx, "file:///never-published.py")
	if err == nil {
		t.Fatal("expected Wait to return an error on context deadline")
	}
}
