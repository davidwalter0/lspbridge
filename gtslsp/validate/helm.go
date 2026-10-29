package validate

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// Helm checks a chart template two ways: `helm lint` and `helm template`, then
// feeds the rendered manifests to kubeconform. Findings are mapped back to the
// template line where helm names one; a finding with no line goes on line 0.
// The chart is copied to a temp dir with the unsaved text overlaid, so the
// on-disk chart is never touched.
type Helm struct{}

func (Helm) Name() string { return "helm" }

func (Helm) Applies(doc Document) bool {
	_, inTemplates := chartRoot(doc.Path)
	e := strings.ToLower(filepath.Ext(doc.Path))
	return inTemplates && (e == ".yaml" || e == ".yml" || e == ".tpl")
}

func (Helm) Available() (bool, string) {
	return lookPathHint("helm", "exttool: helm is not declared; install helm v3 into ~/.local/bin")
}

// maxChartCopy bounds the temp copy of a chart.
const maxChartCopy = 64 << 20

func copyChart(src, dst string) error {
	var total int64
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(filepath.Join(dst, rel))
		if err != nil {
			return err
		}
		n, err := io.Copy(out, in)
		out.Close()
		total += n
		if err == nil && total > maxChartCopy {
			err = fmt.Errorf("chart larger than %d bytes", maxChartCopy)
		}
		return err
	})
}

var (
	helmLintRe = regexp.MustCompile(`^\[(ERROR|WARNING|INFO)\] (\S+?): (.*)$`)
	helmLocRe  = regexp.MustCompile(`([^\s:]*templates/[^\s:]+):(\d+)(?::(\d+))?`)
	helmErrRe  = regexp.MustCompile(`^Error: (.*)$`)
)

type helmFinding struct {
	sev       lsp.DiagnosticSeverity
	line, col int // 0-based
	msg       string
}

// parseHelmMessage extracts the template file:line:col a helm message names.
func parseHelmMessage(msg string) (file string, line, col int, ok bool) {
	m := helmLocRe.FindStringSubmatch(msg)
	if m == nil {
		return "", 0, 0, false
	}
	line, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		col, _ = strconv.Atoi(m[3])
	}
	i := strings.Index(m[1], "templates/")
	return m[1][i:], line - 1, max(col-1, 0), true
}

func (Helm) Validate(ctx context.Context, doc Document) ([]lsp.Diagnostic, error) {
	root, _ := chartRoot(doc.Path)
	rel, err := filepath.Rel(root, doc.Path)
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "gts-lsp-helm-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	chart := filepath.Join(tmp, filepath.Base(root))
	if err := os.MkdirAll(chart, 0o755); err != nil {
		return nil, err
	}
	if err := copyChart(root, chart); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(chart, rel), doc.Text, 0o600); err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)
	lines := splitLines(doc.Text)

	var out []lsp.Diagnostic
	seen := map[int]bool{} // error lines already reported
	add := func(f helmFinding) {
		out = append(out, mkDiag(lines, f.line, f.col, f.sev, "helm", "", f.msg))
	}

	// 1. helm lint
	lint, err := runTool(ctx, tmp, offlineEnv, nil, "helm", "lint", chart)
	if err != nil {
		return nil, err
	}
	for _, l := range splitLines(lint.Stdout) {
		m := helmLintRe.FindStringSubmatch(l)
		if m == nil || m[1] == "INFO" {
			continue
		}
		sev := lsp.SeverityError
		if m[1] == "WARNING" {
			sev = lsp.SeverityWarning
		}
		where, msg := m[2], m[3]
		if f, ln, col, ok := parseHelmMessage(msg); ok {
			if f != rel && !strings.HasSuffix(f, "/"+rel) {
				continue // another template's finding
			}
			add(helmFinding{sev, ln, col, msg})
			if sev == lsp.SeverityError {
				seen[ln] = true
			}
			continue
		}
		switch {
		case where == rel:
			add(helmFinding{sev, 0, 0, msg})
		case where == "templates/":
			add(helmFinding{sev, 0, 0, msg}) // directory-level finding, no file named
		}
	}

	// 2. helm template, then 3. kubeconform on the rendered output
	tpl, err := runTool(ctx, tmp, offlineEnv, nil, "helm", "template", chart)
	if err != nil {
		return nil, err
	}
	if tpl.Exit != 0 {
		for _, l := range splitLines(tpl.Stderr) {
			m := helmErrRe.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			if f, ln, col, ok := parseHelmMessage(m[1]); ok {
				if (f == rel || strings.HasSuffix(f, "/"+rel)) && !seen[ln] {
					add(helmFinding{lsp.SeverityError, ln, col, m[1]})
				}
			}
		}
		return out, nil
	}
	if ok, _ := (Kubeconform{}).Available(); !ok {
		return out, nil
	}
	rs, err := runKubeconform(ctx, tmp, tpl.Stdout)
	if err != nil {
		return out, nil // rendering succeeded; a kubeconform failure is not a chart finding
	}
	chunks := splitRendered(string(tpl.Stdout))
	for i, r := range rs {
		c := matchChunk(chunks, r, i)
		if c == nil || !(c.source == rel || strings.HasSuffix(c.source, "/"+rel)) {
			continue
		}
		switch r.Status {
		case "statusInvalid":
			for _, ve := range r.ValidationErrors {
				segs := refineExtra(c.node, segsFromPointer(ve.Path), ve.Msg)
				ln := templateLine(lines, segs)
				add(helmFinding{lsp.SeverityError, ln, 0, "rendered manifest " + ve.Path + ": " + ve.Msg})
			}
		case "statusError":
			add(helmFinding{lsp.SeverityError, 0, 0, "rendered manifest: " + r.Msg})
		}
	}
	return out, nil
}
