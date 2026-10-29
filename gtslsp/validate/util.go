package validate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// defaultTimeout bounds every external tool run when the caller's context
// carries no deadline of its own.
const defaultTimeout = 30 * time.Second

// offlineEnv makes any accidental network use by a tool fail fast instead of
// fetching: validation never touches the network (schemas come from the
// local cache, see SyncSchemas).
var offlineEnv = []string{
	"HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9",
	"http_proxy=http://127.0.0.1:9", "https_proxy=http://127.0.0.1:9",
	"NO_PROXY=", "no_proxy=",
}

type runResult struct {
	Stdout, Stderr []byte
	Exit           int
}

// runTool executes name with args in dir, feeding stdin, under a context
// timeout. A non-zero exit is NOT an error (linters exit non-zero on
// findings); err is set only when the process could not be run or timed out.
func runTool(ctx context.Context, dir string, extraEnv []string, stdin []byte, name string, args ...string) (runResult, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	res := runResult{Stdout: so.Bytes(), Stderr: se.Bytes()}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		res.Exit = ee.ExitCode()
		if ctx.Err() != nil {
			return res, fmt.Errorf("%s: %w", name, ctx.Err())
		}
	default:
		return res, fmt.Errorf("%s: %w", name, err)
	}
	return res, nil
}

func lookPathHint(bin, hint string) (bool, string) {
	if _, err := exec.LookPath(bin); err != nil {
		return false, hint
	}
	return true, ""
}

func hintFor(tool string) string {
	return "exttool install --tool " + tool + " --confirm"
}

// splitLines splits text into lines without terminators.
func splitLines(text []byte) []string {
	s := strings.ReplaceAll(string(text), "\r\n", "\n")
	return strings.Split(s, "\n")
}

// utf16Len is the length of s in UTF-16 code units, LSP's default encoding.
func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// utf16Col converts a 0-based rune column on line to UTF-16 code units.
func utf16Col(line string, runeCol int) int {
	r := []rune(line)
	if runeCol > len(r) {
		runeCol = len(r)
	}
	if runeCol < 0 {
		runeCol = 0
	}
	return utf16Len(string(r[:runeCol]))
}

// mkDiag builds a diagnostic on a 0-based line starting at 0-based rune column
// col and running to the end of that line. A line outside the document clamps
// to the last line.
func mkDiag(lines []string, line, col int, sev lsp.DiagnosticSeverity, source, code, msg string) lsp.Diagnostic {
	if line >= len(lines) {
		line = len(lines) - 1
	}
	if line < 0 {
		line = 0
	}
	text := ""
	if line < len(lines) {
		text = lines[line]
	}
	start := utf16Col(text, col)
	end := utf16Len(text)
	if end <= start {
		end = start + 1
	}
	d := lsp.Diagnostic{
		Range: lsp.Range{
			Start: lsp.Position{Line: line, Character: start},
			End:   lsp.Position{Line: line, Character: end},
		},
		Severity: sev,
		Source:   source,
		Message:  msg,
	}
	if code != "" {
		d.Code = code
	}
	return d
}

// writeTemp writes doc.Text into a fresh temp dir under the document's own
// base name (tools pick parsers by extension) and returns the file path and a
// cleanup func.
func writeTemp(doc Document) (string, func(), error) {
	dir, err := os.MkdirTemp("", "gts-lsp-validate-")
	if err != nil {
		return "", nil, err
	}
	p := filepath.Join(dir, filepath.Base(doc.Path))
	if err := os.WriteFile(p, doc.Text, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return p, func() { _ = os.RemoveAll(dir) }, nil
}

func isYAML(doc Document) bool {
	if doc.Language == "yaml" {
		return true
	}
	e := strings.ToLower(filepath.Ext(doc.Path))
	return e == ".yaml" || e == ".yml"
}

func isJSON(doc Document) bool {
	if doc.Language == "json" {
		return true
	}
	return strings.ToLower(filepath.Ext(doc.Path)) == ".json"
}

// chartRoot returns the nearest ancestor directory of path holding a
// Chart.yaml, but only when path sits below that chart's templates/ dir.
func chartRoot(path string) (string, bool) {
	dir := filepath.Dir(path)
	inTemplates := false
	for d := dir; ; d = filepath.Dir(d) {
		if filepath.Base(d) == "templates" {
			inTemplates = true
		}
		if _, err := os.Stat(filepath.Join(d, "Chart.yaml")); err == nil {
			return d, inTemplates
		}
		if filepath.Dir(d) == d {
			return "", false
		}
	}
}

func isHelmTemplate(doc Document) bool {
	_, ok := chartRoot(doc.Path)
	return ok
}

// All returns every validator, in a stable order.
func All() []Validator {
	return []Validator{
		Yamllint{}, CheckJSONSchema{}, Kubeconform{}, Helm{}, Taplo{}, Hadolint{},
	}
}
