package validate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// useTestCache points the offline schema cache at the schemas vendored under
// testdata/schemas, so no test touches the network.
func useTestCache(t *testing.T) {
	t.Helper()
	cache := t.TempDir()
	dst := filepath.Join(cache, "gts-lsp", "schemas")
	if err := os.CopyFS(dst, os.DirFS("testdata/schemas")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", cache)
}

func load(t *testing.T, rel, lang string) Document {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", rel))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return Document{Path: p, Language: lang, Text: b}
}

func requireTool(t *testing.T, v Validator) {
	t.Helper()
	if ok, hint := v.Available(); !ok {
		t.Skipf("%s is not installed (genuinely absent); install: %s", v.Name(), hint)
	}
}

func TestValidatorsSeededErrors(t *testing.T) {
	useTestCache(t)
	tests := []struct {
		name     string
		v        Validator
		file     string
		lang     string
		wantLine int    // 0-based line the diagnostic must land on
		wantSub  string // substring of the message
		wantSev  lsp.DiagnosticSeverity
	}{
		{"yamllint trailing spaces", Yamllint{}, "trailing.yaml", "yaml", 2, "trailing spaces", lsp.SeverityError},
		{"schema violation (yaml modeline, local schema)", CheckJSONSchema{}, "port_bad.yaml", "yaml", 2, "not of type 'integer'", lsp.SeverityError},
		{"schema violation (json $schema, local schema)", CheckJSONSchema{}, "port_bad.json", "json", 1, "not of type 'integer'", lsp.SeverityError},
		{"kubeconform invalid Deployment field", Kubeconform{}, "deploy_bad.yaml", "yaml", 5, "/spec/replicas", lsp.SeverityError},
		{"helm nil .Values reference", Helm{}, "chartbadvalues/templates/deployment.yaml", "yaml", 16, "nil pointer", lsp.SeverityError},
		{"helm invalid rendered manifest", Helm{}, "chartbadmanifest/templates/deployment.yaml", "yaml", 5, "rendered manifest /spec/replicas", lsp.SeverityError},
		{"taplo syntax error", Taplo{}, "bad.toml", "toml", 4, "expected", lsp.SeverityError},
		{"hadolint DL3007 latest tag", Hadolint{}, "Dockerfile.bad", "dockerfile", 0, "latest", lsp.SeverityWarning},
		{"hadolint DL3008 unpinned apt", Hadolint{}, "Dockerfile.bad", "dockerfile", 1, "Pin versions", lsp.SeverityWarning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireTool(t, tt.v)
			if tt.v.Name() == "helm" {
				requireTool(t, Kubeconform{})
			}
			doc := load(t, tt.file, tt.lang)
			if !tt.v.Applies(doc) {
				t.Fatalf("%s.Applies(%s) = false", tt.v.Name(), tt.file)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			ds, err := tt.v.Validate(ctx, doc)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			for _, d := range ds {
				if d.Source != tt.v.Name() {
					t.Errorf("Source = %q, want %q", d.Source, tt.v.Name())
				}
				if d.Range.Start.Line == tt.wantLine && strings.Contains(d.Message, tt.wantSub) {
					if d.Severity != tt.wantSev {
						t.Errorf("severity = %d, want %d", d.Severity, tt.wantSev)
					}
					return
				}
			}
			t.Fatalf("no diagnostic on line %d containing %q; got %+v", tt.wantLine, tt.wantSub, ds)
		})
	}
}

func TestCleanDocumentsYieldNothing(t *testing.T) {
	useTestCache(t)
	tests := []struct {
		name string
		v    Validator
		text string
		path string
		lang string
	}{
		{"yamllint", Yamllint{}, "---\na: 1\n", "ok.yaml", "yaml"},
		{"taplo", Taplo{}, "a = 1\n", "ok.toml", "toml"},
		{"hadolint", Hadolint{}, "FROM scratch\n", "Dockerfile", "dockerfile"},
		{"check-jsonschema", CheckJSONSchema{}, "# yaml-language-server: $schema=./schema.json\nname: x\nport: 80\n", "ok.yaml", "yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireTool(t, tt.v)
			dir, _ := filepath.Abs("testdata")
			doc := Document{Path: filepath.Join(dir, tt.path), Language: tt.lang, Text: []byte(tt.text)}
			ds, err := tt.v.Validate(context.Background(), doc)
			if err != nil || len(ds) != 0 {
				t.Fatalf("got %+v, %v; want none", ds, err)
			}
		})
	}
}

// The unsaved buffer, not the file on disk, is what gets validated.
func TestValidatesBufferNotDisk(t *testing.T) {
	requireTool(t, Yamllint{})
	doc := load(t, "trailing.yaml", "yaml")
	doc.Text = []byte("---\na: 1\n") // clean buffer over a dirty file
	ds, err := Yamllint{}.Validate(context.Background(), doc)
	if err != nil || len(ds) != 0 {
		t.Fatalf("got %+v, %v; want none", ds, err)
	}
}

// A remote schema that is not in the cache is skipped, never fetched.
func TestRemoteUncachedSchemaIsNotFetched(t *testing.T) {
	requireTool(t, CheckJSONSchema{})
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	dir, _ := filepath.Abs("testdata")
	doc := Document{Path: filepath.Join(dir, "r.yaml"), Language: "yaml",
		Text: []byte("# yaml-language-server: $schema=" + srv.URL + "/s.json\nname: 1\n")}
	ds, err := CheckJSONSchema{}.Validate(context.Background(), doc)
	if err != nil || hits != 0 {
		t.Fatalf("err=%v server hits=%d; want nil, 0", err, hits)
	}
	// ...but never silently: one Information diagnostic names URL and command.
	if len(ds) != 1 || ds[0].Severity != lsp.SeverityInformation || ds[0].Source != "check-jsonschema" ||
		ds[0].Range.Start.Line != 0 || !strings.Contains(ds[0].Message, srv.URL+"/s.json") ||
		!strings.Contains(ds[0].Message, "gts-lsp schemas sync") {
		t.Fatalf("diags = %+v; want one Information naming the URL and the sync command", ds)
	}
}

// An empty cache must yield the Information diagnostic, never nothing.
func TestKubeconformUncachedIsInformation(t *testing.T) {
	requireTool(t, Kubeconform{})
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	doc := load(t, "deploy_bad.yaml", "yaml")
	ds, err := Kubeconform{}.Validate(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 || ds[0].Severity != lsp.SeverityInformation || ds[0].Source != "kubeconform" ||
		ds[0].Range.Start.Line != 0 ||
		!strings.Contains(ds[0].Message, "no cached schema for apps/v1 Deployment (Kubernetes "+k8sVersion()+")") ||
		!strings.Contains(ds[0].Message, "run: gts-lsp schemas sync") {
		t.Fatalf("diags = %+v", ds)
	}
}

func TestK8sSchemaURLs(t *testing.T) {
	urls, err := K8sSchemaURLs("1.31.0", []string{"deployment", "Ingress", "ClusterRoleBinding", "HorizontalPodAutoscaler", "Pod", "Gateway=gateway.networking.k8s.io/v1"})
	if err != nil {
		t.Fatal(err)
	}
	base := k8sSchemaBase + "/v1.31.0-standalone-strict/"
	want := []string{"deployment-apps-v1.json", "ingress-networking-v1.json", "clusterrolebinding-rbac-v1.json",
		"horizontalpodautoscaler-autoscaling-v2.json", "pod-v1.json", "gateway-gateway-v1.json"}
	for i, w := range want {
		if urls[i] != base+w {
			t.Errorf("urls[%d] = %s, want %s", i, urls[i], base+w)
		}
		// The cache layout must be what runKubeconform's template reads.
		t.Setenv("XDG_CACHE_HOME", "/c")
		if got := cachePathForURL(urls[i]); got != "/c/gts-lsp/schemas/k8s/v1.31.0-standalone-strict/"+w {
			t.Errorf("cache path %s", got)
		}
	}
	if all, _ := K8sSchemaURLs("", nil); len(all) != 19 {
		t.Errorf("default set has %d kinds, want 19", len(all))
	}
	if _, err := K8sSchemaURLs("", []string{"Nope"}); err == nil {
		t.Error("unknown kind accepted")
	}
}

func TestSyncWithReportFailsOnHTTPError(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok.json" {
			_, _ = w.Write([]byte("{}"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	res, err := SyncWithReport(context.Background(), []string{srv.URL + "/ok.json", srv.URL + "/missing.json"})
	if err == nil || len(res) != 1 || res[0].Bytes != 2 {
		t.Fatalf("res=%+v err=%v; want 1 success and an error", res, err)
	}
}

func TestYamllintDefaultConfig(t *testing.T) {
	requireTool(t, Yamllint{})
	t.Setenv("YAMLLINT_CONFIG_FILE", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	body := []byte("a: 1\n")
	if err := os.WriteFile(filepath.Join(dir, "x.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	doc := Document{Path: filepath.Join(dir, "x.yaml"), Language: "yaml", Text: body}
	// No project config: relaxed default, no document-start nag.
	if ds, err := (Yamllint{}).Validate(context.Background(), doc); err != nil || len(ds) != 0 {
		t.Fatalf("no config: got %+v, %v; want none", ds, err)
	}
	// A project's own .yamllint wins: here it re-enables document-start.
	cfg := "extends: default\nrules:\n  document-start: enable\n"
	if err := os.WriteFile(filepath.Join(dir, ".yamllint"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	ds, err := Yamllint{}.Validate(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range ds {
		found = found || d.Code == "document-start"
	}
	if !found {
		t.Fatalf("project .yamllint ignored: %+v", ds)
	}
}

// SyncSchemas is the explicit fetch; afterwards the cached copy is used.
func TestSyncSchemasThenValidate(t *testing.T) {
	requireTool(t, CheckJSONSchema{})
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	schema, err := os.ReadFile("testdata/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(schema) }))
	defer srv.Close()
	url := srv.URL + "/s.json"
	if err := SyncSchemas(context.Background(), []string{url}); err != nil {
		t.Fatal(err)
	}
	dir, _ := filepath.Abs("testdata")
	doc := Document{Path: filepath.Join(dir, "r.yaml"), Language: "yaml",
		Text: []byte("# yaml-language-server: $schema=" + url + "\nname: x\nport: nope\n")}
	ds, err := CheckJSONSchema{}.Validate(context.Background(), doc)
	if err != nil || len(ds) != 1 || ds[0].Range.Start.Line != 2 {
		t.Fatalf("got %+v, %v; want one diagnostic on line 2", ds, err)
	}
	if err := SyncSchemas(context.Background(), []string{"file:///etc/passwd"}); err == nil {
		t.Fatal("SyncSchemas accepted a non-http URL")
	}
}

func TestUTF16Columns(t *testing.T) {
	// "😀" is one rune, two UTF-16 units: a rune column after it shifts by one.
	lines := []string{"a: 😀 x"}
	d := mkDiag(lines, 0, 5, lsp.SeverityError, "t", "", "m")
	if d.Range.Start.Character != 6 || d.Range.End.Character != 7 {
		t.Fatalf("range = %+v, want chars 6..7", d.Range)
	}
}

func TestAllAndHints(t *testing.T) {
	want := []string{"yamllint", "check-jsonschema", "kubeconform", "helm", "taplo", "hadolint"}
	all := All()
	if len(all) != len(want) {
		t.Fatalf("All() has %d validators, want %d", len(all), len(want))
	}
	for i, v := range all {
		if v.Name() != want[i] {
			t.Errorf("All()[%d] = %q, want %q", i, v.Name(), want[i])
		}
	}
	t.Setenv("PATH", t.TempDir())
	for _, v := range all {
		ok, hint := v.Available()
		if ok || hint == "" {
			t.Errorf("%s with empty PATH: ok=%v hint=%q", v.Name(), ok, hint)
		}
		if v.Name() != "helm" && !strings.Contains(hint, "exttool install --tool "+v.Name()) {
			t.Errorf("%s hint %q does not name the exttool command", v.Name(), hint)
		}
	}
}

func TestApplies(t *testing.T) {
	tests := []struct {
		v    Validator
		path string
		lang string
		want bool
	}{
		{Yamllint{}, "/x/a.yml", "", true},
		{Yamllint{}, "/x/a.toml", "toml", false},
		{Taplo{}, "/x/Cargo.toml", "", true},
		{Hadolint{}, "/x/Dockerfile.dev", "", true},
		{Hadolint{}, "/x/a.yaml", "yaml", false},
		{Helm{}, "/x/templates/a.yaml", "yaml", false}, // no Chart.yaml above
	}
	for _, tt := range tests {
		if got := tt.v.Applies(Document{Path: tt.path, Language: tt.lang}); got != tt.want {
			t.Errorf("%s.Applies(%s,%q) = %v, want %v", tt.v.Name(), tt.path, tt.lang, got, tt.want)
		}
	}
}
