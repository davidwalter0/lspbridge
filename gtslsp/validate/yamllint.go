package validate

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// Yamllint lints YAML style and syntax with `yamllint -f parsable`.
type Yamllint struct{}

func (Yamllint) Name() string { return "yamllint" }

func (Yamllint) Applies(doc Document) bool { return isYAML(doc) && !isHelmTemplate(doc) }

func (Yamllint) Available() (bool, string) {
	return lookPathHint("yamllint", hintFor("yamllint"))
}

var yamllintRe = regexp.MustCompile(`^stdin:(\d+):(\d+): \[(error|warning)\] (.*?)(?: \(([\w-]+)\))?$`)

// Validate feeds the (possibly unsaved) text on stdin and runs in the
// document's directory so a nearby .yamllint config applies.
func (Yamllint) Validate(ctx context.Context, doc Document) ([]lsp.Diagnostic, error) {
	res, err := runTool(ctx, filepath.Dir(doc.Path), nil, doc.Text, "yamllint", "-f", "parsable", "-")
	if err != nil {
		return nil, err
	}
	lines := splitLines(doc.Text)
	var out []lsp.Diagnostic
	for _, l := range splitLines(res.Stdout) {
		m := yamllintRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		ln, _ := strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])
		sev := lsp.SeverityError
		if m[3] == "warning" {
			sev = lsp.SeverityWarning
		}
		out = append(out, mkDiag(lines, ln-1, col-1, sev, "yamllint", m[5], m[4]))
	}
	return out, nil
}
