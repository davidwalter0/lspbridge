package validate

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// Hadolint lints Dockerfiles with `hadolint --format json`.
type Hadolint struct{}

func (Hadolint) Name() string { return "hadolint" }

func (Hadolint) Applies(doc Document) bool {
	if doc.Language == "dockerfile" {
		return true
	}
	// The filename fallback is only for a document no grammar claimed: a
	// dockerfile.go or containerfile_test.go is source, not a Dockerfile.
	if doc.Language != "" {
		return false
	}
	b := strings.ToLower(filepath.Base(doc.Path))
	return strings.HasPrefix(b, "dockerfile") || strings.HasPrefix(b, "containerfile") ||
		strings.HasSuffix(b, ".dockerfile")
}

func (Hadolint) Available() (bool, string) { return lookPathHint("hadolint", hintFor("hadolint")) }

func (Hadolint) Validate(ctx context.Context, doc Document) ([]lsp.Diagnostic, error) {
	res, err := runTool(ctx, filepath.Dir(doc.Path), nil, doc.Text, "hadolint", "--format", "json", "-")
	if err != nil {
		return nil, err
	}
	var findings []struct {
		Code    string `json:"code"`
		Column  int    `json:"column"`
		Level   string `json:"level"`
		Line    int    `json:"line"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(res.Stdout, &findings); err != nil {
		return nil, fmt.Errorf("hadolint: exit %d: %s", res.Exit, strings.TrimSpace(string(res.Stderr)))
	}
	lines := splitLines(doc.Text)
	var out []lsp.Diagnostic
	for _, f := range findings {
		sev := lsp.SeverityInformation
		switch f.Level {
		case "error":
			sev = lsp.SeverityError
		case "warning":
			sev = lsp.SeverityWarning
		case "style":
			sev = lsp.SeverityHint
		}
		out = append(out, mkDiag(lines, f.Line-1, f.Column-1, sev, "hadolint", f.Code, f.Message))
	}
	return out, nil
}
