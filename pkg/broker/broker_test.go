package broker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"github.com/davidwalter0/lspbridge/pkg/projectcontext"
	"github.com/davidwalter0/lspbridge/pkg/server"
)

// fakeLSPBin is the freshly built fakelsp helper server.
var fakeLSPBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "lspbridge-broker-fakelsp")
	if err != nil {
		panic(err)
	}
	bin := filepath.Join(dir, "fakelsp")
	build := exec.Command("go", "build", "-o", bin, "github.com/davidwalter0/lspbridge/cmd/fakelsp")
	if out, err := build.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		panic("build fakelsp: " + err.Error() + "\n" + string(out))
	}
	fakeLSPBin = bin
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// fakeResolver maps any .py file to a python Context rooted at root.
type fakeResolver struct{ root string }

func (r fakeResolver) Resolve(abs string) (projectcontext.Context, bool) {
	if filepath.Ext(abs) == ".py" {
		return projectcontext.Context{Root: r.root, Language: "python"}, true
	}
	return projectcontext.Context{}, false
}

// testClock is a manually advanced clock for deterministic idle-reap tests.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// newFakeBroker returns a broker whose sessions are fakelsp subprocesses, plus
// a launch counter and the manual clock.
func newFakeBroker(t *testing.T) (*Broker, *atomic.Int64, *testClock) {
	t.Helper()
	root := t.TempDir()
	var launches atomic.Int64
	clock := &testClock{t: time.Unix(1_700_000_000, 0)}
	b := New(Config{
		Resolver: fakeResolver{root: root},
		SpecFor: func(pc projectcontext.Context) (server.Spec, error) {
			launches.Add(1)
			return server.Spec{Command: fakeLSPBin, Dir: pc.Root}, nil
		},
		IdleTimeout: 10 * time.Minute,
		Clock:       clock.now,
	})
	t.Cleanup(b.ShutdownAll)
	return b, &launches, clock
}

func docSymbolQuery(root string) QueryRequest {
	text := "x = 1\n"
	params, _ := json.Marshal(lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: lsp.DocumentURI("file://" + filepath.Join(root, "a.py"))},
	})
	return QueryRequest{
		File:   filepath.Join(root, "a.py"),
		Method: "textDocument/documentSymbol",
		Params: params,
		Text:   &text,
	}
}

func TestBrokerQueryForwards(t *testing.T) {
	b, launches, _ := newFakeBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	root := b.resolver.(fakeResolver).root
	raw, err := b.Query(ctx, docSymbolQuery(root))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var syms []lsp.DocumentSymbol
	if err := json.Unmarshal(raw, &syms); err != nil {
		t.Fatalf("decode result %s: %v", raw, err)
	}
	if len(syms) != 1 || syms[0].Name != "cannedSymbol" {
		t.Fatalf("documentSymbol result = %+v", syms)
	}
	if got := launches.Load(); got != 1 {
		t.Fatalf("launches = %d, want 1", got)
	}
}

func TestBrokerSingleFlight(t *testing.T) {
	b, launches, _ := newFakeBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root := b.resolver.(fakeResolver).root

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = b.Query(ctx, docSymbolQuery(root))
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Query[%d]: %v", i, err)
		}
	}
	// All N concurrent queries for the same (root,language) key must share ONE
	// cold start.
	if got := launches.Load(); got != 1 {
		t.Fatalf("launches = %d, want 1 (single-flight)", got)
	}
	// Second-open of the same doc goes through didChange, not a second didOpen —
	// exercised implicitly; assert the session is warm and singular.
	st := b.Status()
	if len(st.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(st.Sessions))
	}
}

func TestBrokerStatus(t *testing.T) {
	b, _, _ := newFakeBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	root := b.resolver.(fakeResolver).root

	if st := b.Status(); len(st.Sessions) != 0 {
		t.Fatalf("cold broker has %d sessions, want 0", len(st.Sessions))
	}
	if _, err := b.Query(ctx, docSymbolQuery(root)); err != nil {
		t.Fatalf("Query: %v", err)
	}
	st := b.Status()
	if len(st.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(st.Sessions))
	}
	s := st.Sessions[0]
	if s.Language != "python" || s.Root != root {
		t.Fatalf("session = %+v", s)
	}
	wantURI := "file://" + filepath.Join(root, "a.py")
	if len(s.OpenDocs) != 1 || s.OpenDocs[0] != wantURI {
		t.Fatalf("openDocs = %v, want [%s]", s.OpenDocs, wantURI)
	}
	if s.Inflight != 0 {
		t.Fatalf("inflight = %d, want 0 after query returned", s.Inflight)
	}
}

func TestBrokerIdleReap(t *testing.T) {
	b, _, clock := newFakeBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	root := b.resolver.(fakeResolver).root

	if _, err := b.Query(ctx, docSymbolQuery(root)); err != nil {
		t.Fatalf("Query: %v", err)
	}

	// Not yet idle: reap does nothing.
	if reaped := b.reapIdle(clock.now()); len(reaped) != 0 {
		t.Fatalf("reaped %d sessions while fresh, want 0", len(reaped))
	}
	if len(b.Status().Sessions) != 1 {
		t.Fatal("session vanished before idle timeout")
	}

	// Past the idle timeout: reaped.
	clock.advance(11 * time.Minute)
	reaped := b.reapIdle(clock.now())
	if len(reaped) != 1 || reaped[0].Root != root {
		t.Fatalf("reaped = %+v, want the one python session", reaped)
	}
	if len(b.Status().Sessions) != 0 {
		t.Fatal("reaped session still present in status")
	}
}

func TestBrokerShutdownAllClosed(t *testing.T) {
	b, _, _ := newFakeBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	root := b.resolver.(fakeResolver).root

	if _, err := b.Query(ctx, docSymbolQuery(root)); err != nil {
		t.Fatalf("Query: %v", err)
	}
	b.ShutdownAll()
	b.ShutdownAll() // idempotent
	if _, err := b.Query(ctx, docSymbolQuery(root)); err != ErrClosed {
		t.Fatalf("Query after shutdown = %v, want ErrClosed", err)
	}
}

func TestResolveContextErrors(t *testing.T) {
	b := New(Config{Resolver: fakeResolver{root: "/proj"}})
	t.Cleanup(b.ShutdownAll)

	if _, err := b.resolveContext(QueryRequest{File: ""}); err == nil {
		t.Error("empty file should error")
	}
	if _, err := b.resolveContext(QueryRequest{File: "rel/a.py"}); err == nil {
		t.Error("relative file should error")
	}
	// No resolver match (.go, not .py) and no language override.
	if _, err := b.resolveContext(QueryRequest{File: "/x/a.go"}); err == nil {
		t.Error("unmatched file with no language should error")
	}
	// No resolver match but a language override → roots at file dir.
	pc, err := b.resolveContext(QueryRequest{File: "/x/a.go", Language: "go"})
	if err != nil {
		t.Fatalf("language-override resolve: %v", err)
	}
	if pc.Language != "go" || pc.Root != "/x" {
		t.Fatalf("override context = %+v", pc)
	}
	// Language override on a matched file replaces the resolved languageId.
	pc, err = b.resolveContext(QueryRequest{File: "/proj/a.py", Language: "python2"})
	if err != nil {
		t.Fatalf("matched override resolve: %v", err)
	}
	if pc.Language != "python2" {
		t.Fatalf("override language = %q, want python2", pc.Language)
	}
}

func TestQueryRequiresMethod(t *testing.T) {
	b := New(Config{Resolver: fakeResolver{root: "/proj"}})
	t.Cleanup(b.ShutdownAll)
	if _, err := b.Query(context.Background(), QueryRequest{File: "/proj/a.py"}); err == nil {
		t.Error("query without method should error")
	}
}

func TestDefaultSpecFor(t *testing.T) {
	// One underlying server binary may be absent on a given host; a LookPath
	// error is still an "error", so this only logs for the known languages
	// and asserts distinctly that an unknown language always errors below.
	for _, lang := range []string{"python", "typescript", "typescriptreact", "javascript", "javascriptreact", "rust"} {
		if _, err := DefaultSpecFor(projectcontext.Context{Language: lang, Root: "/p"}); err != nil {
			t.Logf("%s spec (server maybe absent): %v", lang, err)
		}
	}
	if _, err := DefaultSpecFor(projectcontext.Context{Language: "cobol", Root: "/p"}); err == nil {
		t.Error("unknown language should error")
	}
}

func TestBrokerQueryReadsFromDisk(t *testing.T) {
	b, _, _ := newFakeBroker(t)
	root := b.resolver.(fakeResolver).root
	if err := os.WriteFile(filepath.Join(root, "a.py"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req := docSymbolQuery(root)
	req.Text = nil // force the on-disk read path in contentFor
	if _, err := b.Query(ctx, req); err != nil {
		t.Fatalf("Query reading from disk: %v", err)
	}
}

func TestBrokerContentForMissingFile(t *testing.T) {
	b, _, _ := newFakeBroker(t)
	root := b.resolver.(fakeResolver).root
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req := docSymbolQuery(root)
	req.Text = nil
	req.File = filepath.Join(root, "does-not-exist.py")
	if _, err := b.Query(ctx, req); err == nil {
		t.Fatal("expected read error for missing file")
	}
}

func TestBrokerInitFailure(t *testing.T) {
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Skip("/bin/true not available")
	}
	b := New(Config{
		Resolver: fakeResolver{root: t.TempDir()},
		SpecFor: func(projectcontext.Context) (server.Spec, error) {
			// A command that exits immediately is not an LSP server: its stdout
			// closes, so the initialize handshake fails.
			return server.Spec{Command: truePath}, nil
		},
	})
	t.Cleanup(b.ShutdownAll)
	root := b.resolver.(fakeResolver).root
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := b.Query(ctx, docSymbolQuery(root)); err == nil {
		t.Fatal("expected initialize failure from a non-LSP command")
	}
}

func TestBrokerSpecError(t *testing.T) {
	// A SpecFunc that always errors surfaces the failure and drops the
	// placeholder so a retry re-attempts.
	var calls atomic.Int64
	b := New(Config{
		Resolver: fakeResolver{root: t.TempDir()},
		SpecFor: func(projectcontext.Context) (server.Spec, error) {
			calls.Add(1)
			return server.Spec{}, os.ErrPermission
		},
	})
	t.Cleanup(b.ShutdownAll)
	root := b.resolver.(fakeResolver).root
	req := docSymbolQuery(root)
	if _, err := b.Query(context.Background(), req); err == nil {
		t.Fatal("expected spec error")
	}
	if _, err := b.Query(context.Background(), req); err == nil {
		t.Fatal("expected spec error on retry")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("spec calls = %d, want 2 (failed cold start must not cache)", got)
	}
}
