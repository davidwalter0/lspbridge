package validate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"gopkg.in/yaml.v3"
)

// Kubeconform validates Kubernetes manifests against schemas in the offline
// cache (<CacheDir>/k8s/<version>-standalone-strict/). A kind with no cached
// schema is skipped, never fetched.
type Kubeconform struct{}

func (Kubeconform) Name() string { return "kubeconform" }

func (Kubeconform) Applies(doc Document) bool {
	return isYAML(doc) && !isHelmTemplate(doc) && looksLikeK8s(doc.Text)
}

func (Kubeconform) Available() (bool, string) {
	return lookPathHint("kubeconform", hintFor("kubeconform"))
}

// k8sVersion is the schema version; GTS_LSP_K8S_VERSION overrides.
func k8sVersion() string {
	if v := os.Getenv("GTS_LSP_K8S_VERSION"); v != "" {
		return strings.TrimPrefix(v, "v")
	}
	return "1.31.0"
}

func looksLikeK8s(text []byte) bool {
	for _, d := range yamlDocs(text) {
		r := docRoot(d)
		_, av := mapGet(r, "apiVersion")
		_, kd := mapGet(r, "kind")
		if av != nil && kd != nil && av.Kind == yaml.ScalarNode && kd.Kind == yaml.ScalarNode {
			return true
		}
	}
	return false
}

type kcResource struct {
	Kind, Name, Status, Msg string
	ValidationErrors        []struct {
		Path string `json:"path"`
		Msg  string `json:"msg"`
	} `json:"validationErrors"`
}

// runKubeconform validates multi-document text read on stdin.
func runKubeconform(ctx context.Context, dir string, text []byte) ([]kcResource, error) {
	tmpl := filepath.Join(CacheDir(), "k8s") +
		"/{{.NormalizedKubernetesVersion}}-standalone{{.StrictSuffix}}/{{.ResourceKind}}{{.KindSuffix}}.json"
	res, err := runTool(ctx, dir, offlineEnv, text, "kubeconform",
		"-kubernetes-version", k8sVersion(), "-output", "json", "-strict",
		"-ignore-missing-schemas", "-schema-location", tmpl, "-")
	if err != nil {
		return nil, err
	}
	var o struct {
		Resources []kcResource `json:"resources"`
	}
	if err := json.Unmarshal(res.Stdout, &o); err != nil {
		return nil, fmt.Errorf("kubeconform: exit %d: %s", res.Exit, strings.TrimSpace(string(res.Stderr)))
	}
	return o.Resources, nil
}

// docName returns the metadata.name of a manifest document.
func docName(d *yaml.Node) string {
	_, md := mapGet(docRoot(d), "metadata")
	if _, n := mapGet(md, "name"); n != nil {
		return n.Value
	}
	return ""
}

func docKind(d *yaml.Node) string {
	if _, k := mapGet(docRoot(d), "kind"); k != nil {
		return k.Value
	}
	return ""
}

// findDoc picks the document a kubeconform resource refers to: by kind+name,
// else by position.
func findDoc(docs []*yaml.Node, r kcResource, idx int) *yaml.Node {
	for _, d := range docs {
		if docKind(d) == r.Kind && docName(d) == r.Name {
			return d
		}
	}
	if idx < len(docs) {
		return docs[idx]
	}
	return nil
}

func (Kubeconform) Validate(ctx context.Context, doc Document) ([]lsp.Diagnostic, error) {
	rs, err := runKubeconform(ctx, filepath.Dir(doc.Path), doc.Text)
	if err != nil {
		return nil, err
	}
	lines := splitLines(doc.Text)
	docs := yamlDocs(doc.Text)
	var out []lsp.Diagnostic
	for i, r := range rs {
		d := findDoc(docs, r, i)
		switch r.Status {
		case "statusInvalid":
			for _, ve := range r.ValidationErrors {
				line, col := 0, 0
				if d != nil {
					segs := refineExtra(d, segsFromPointer(ve.Path), ve.Msg)
					line, col, _ = locate(d, segs)
				}
				out = append(out, mkDiag(lines, line, col, lsp.SeverityError, "kubeconform", "", ve.Path+": "+ve.Msg))
			}
			if len(r.ValidationErrors) == 0 {
				out = append(out, mkDiag(lines, docLine(d), 0, lsp.SeverityError, "kubeconform", "", r.Msg))
			}
		case "statusError":
			out = append(out, mkDiag(lines, docLine(d), 0, lsp.SeverityError, "kubeconform", "", r.Msg))
		}
	}
	return out, nil
}

func docLine(d *yaml.Node) int {
	if r := docRoot(d); r != nil && r.Line > 0 {
		return r.Line - 1
	}
	return 0
}
