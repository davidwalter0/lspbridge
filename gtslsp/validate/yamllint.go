package validate

import (
	"context"
	"os"
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

// defaultYamllintConfig applies only when no yamllint config is found. Plain
// yamllint's default demands a `---` document start and 80-column lines, which
// fire on nearly every Kubernetes/Helm file and that yaml-language-server users
// never see. `relaxed` drops the style noise; document-start is disabled
// explicitly because relaxed still warns on it. Syntax errors, duplicate keys
// and trailing spaces stay on. A project's own config always wins.
const defaultYamllintConfig = "{extends: relaxed, rules: {document-start: disable}}"

// hasYamllintConfig mirrors yamllint's own lookup: YAMLLINT_CONFIG_FILE, a
// .yamllint[.yaml|.yml] in dir or any ancestor, then the user-global config.
func hasYamllintConfig(dir string) bool {
	if os.Getenv("YAMLLINT_CONFIG_FILE") != "" {
		return true
	}
	for d := dir; ; d = filepath.Dir(d) {
		for _, n := range []string{".yamllint", ".yamllint.yaml", ".yamllint.yml"} {
			if _, err := os.Stat(filepath.Join(d, n)); err == nil {
				return true
			}
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		if h, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(h, ".config")
		}
	}
	if base != "" {
		if _, err := os.Stat(filepath.Join(base, "yamllint", "config")); err == nil {
			return true
		}
	}
	return false
}

// Validate feeds the (possibly unsaved) text on stdin and runs in the
// document's directory so a nearby .yamllint config applies.
func (Yamllint) Validate(ctx context.Context, doc Document) ([]lsp.Diagnostic, error) {
	dir := filepath.Dir(doc.Path)
	args := []string{"-f", "parsable"}
	if !hasYamllintConfig(dir) {
		args = append(args, "-d", defaultYamllintConfig)
	}
	res, err := runTool(ctx, dir, nil, doc.Text, "yamllint", append(args, "-")...)
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
