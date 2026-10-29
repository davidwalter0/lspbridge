package validate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
	"gopkg.in/yaml.v3"
)

// CheckJSONSchema validates YAML/JSON against the JSON Schema the document
// names (a `$schema` key, or a `# yaml-language-server: $schema=...` modeline).
// The schema must be a local path or already in the offline cache; it is
// never fetched here (see SyncSchemas).
type CheckJSONSchema struct{}

func (CheckJSONSchema) Name() string { return "check-jsonschema" }

func (CheckJSONSchema) Applies(doc Document) bool {
	if !(isYAML(doc) || isJSON(doc)) || isHelmTemplate(doc) {
		return false
	}
	_, ok := schemaRef(doc)
	return ok
}

func (CheckJSONSchema) Available() (bool, string) {
	return lookPathHint("check-jsonschema", hintFor("check-jsonschema"))
}

var modelineRe = regexp.MustCompile(`^#\s*yaml-language-server:\s*\$schema=(\S+)`)

// schemaRef extracts the schema reference a document names.
func schemaRef(doc Document) (string, bool) {
	if isYAML(doc) {
		for i, l := range splitLines(doc.Text) {
			if i > 20 {
				break
			}
			if m := modelineRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
				return m[1], true
			}
		}
	}
	docs := yamlDocs(doc.Text)
	if len(docs) == 0 {
		return "", false
	}
	if _, v := mapGet(docRoot(docs[0]), "$schema"); v != nil && v.Kind == yaml.ScalarNode && v.Value != "" {
		return v.Value, true
	}
	return "", false
}

// resolveSchema turns a reference into a local file, or "" when it is remote
// and not cached.
func resolveSchema(ref, docDir string) string {
	if u, err := url.Parse(ref); err == nil && u.Scheme != "" && len(u.Scheme) > 1 {
		switch u.Scheme {
		case "file":
			return existing(u.Path)
		case "http", "https":
			return existing(cachePathForURL(ref))
		}
		return ""
	}
	if !filepath.IsAbs(ref) {
		ref = filepath.Join(docDir, ref)
	}
	return existing(ref)
}

func existing(p string) string {
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p
	}
	return ""
}

type cjsOutput struct {
	Errors []struct {
		Path    string `json:"path"`
		Message string `json:"message"`
	} `json:"errors"`
	ParseErrors []struct {
		Filename string `json:"filename"`
		Message  string `json:"message"`
	} `json:"parse_errors"`
}

func (CheckJSONSchema) Validate(ctx context.Context, doc Document) ([]lsp.Diagnostic, error) {
	ref, ok := schemaRef(doc)
	if !ok {
		return nil, nil
	}
	schema := resolveSchema(ref, filepath.Dir(doc.Path))
	if schema == "" {
		return nil, nil // remote and not cached: never fetch while validating
	}
	inst, cleanup, err := writeTemp(doc)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	res, err := runTool(ctx, filepath.Dir(inst), offlineEnv, nil, "check-jsonschema",
		"--no-cache", "--output-format", "json", "--schemafile", schema, inst)
	if err != nil {
		return nil, err
	}
	if res.Exit == 0 {
		return nil, nil
	}
	var o cjsOutput
	if err := json.Unmarshal(res.Stdout, &o); err != nil {
		return nil, fmt.Errorf("check-jsonschema: exit %d: %s", res.Exit, strings.TrimSpace(string(res.Stderr)))
	}
	lines := splitLines(doc.Text)
	docs := yamlDocs(doc.Text)
	var out []lsp.Diagnostic
	for _, e := range o.Errors {
		line, col := 0, 0
		if len(docs) > 0 {
			segs := refineExtra(docs[0], segsFromJSONPath(e.Path), e.Message)
			line, col, _ = locate(docs[0], segs)
		}
		out = append(out, mkDiag(lines, line, col, lsp.SeverityError, "check-jsonschema", "", e.Message))
	}
	for _, e := range o.ParseErrors {
		out = append(out, mkDiag(lines, 0, 0, lsp.SeverityError, "check-jsonschema", "", e.Message))
	}
	return out, nil
}
