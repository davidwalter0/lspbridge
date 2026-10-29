package validate

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// Taplo checks TOML syntax and semantics with `taplo lint`. Schema
// validation is disabled (--no-schema): it would fetch schemas.
type Taplo struct{}

func (Taplo) Name() string { return "taplo" }

func (Taplo) Applies(doc Document) bool {
	return doc.Language == "toml" || strings.ToLower(filepath.Ext(doc.Path)) == ".toml"
}

func (Taplo) Available() (bool, string) { return lookPathHint("taplo", hintFor("taplo")) }

var (
	taploHead  = regexp.MustCompile(`^(error|warning)(?:\[\w+\])?: (.*)$`)
	taploPos   = regexp.MustCompile(`^\s*┌─ .*:(\d+):(\d+)\s*$`)
	taploLabel = regexp.MustCompile(`│[ ╭╰─│]*\^+\s+(\S.*)$`)
)

func (Taplo) Validate(ctx context.Context, doc Document) ([]lsp.Diagnostic, error) {
	res, err := runTool(ctx, filepath.Dir(doc.Path), nil, doc.Text,
		"taplo", "lint", "--colors", "never", "--no-auto-config", "--no-schema", "-")
	if err != nil {
		return nil, err
	}
	// taplo prints its rendered report on stderr.
	all := string(res.Stderr) + "\n" + string(res.Stdout)
	lines := splitLines(doc.Text)
	type entry struct {
		sev        lsp.DiagnosticSeverity
		msg, label string
		line, col  int
		havePos    bool
	}
	var cur *entry
	var out []lsp.Diagnostic
	flush := func() {
		if cur == nil || !cur.havePos {
			cur = nil
			return
		}
		msg := cur.msg
		if cur.label != "" {
			msg += ": " + cur.label
		}
		out = append(out, mkDiag(lines, cur.line, cur.col, cur.sev, "taplo", "", msg))
		cur = nil
	}
	for _, l := range strings.Split(all, "\n") {
		if m := taploHead.FindStringSubmatch(l); m != nil {
			flush()
			sev := lsp.SeverityError
			if m[1] == "warning" {
				sev = lsp.SeverityWarning
			}
			cur = &entry{sev: sev, msg: m[2]}
			continue
		}
		if cur == nil {
			continue
		}
		if m := taploPos.FindStringSubmatch(l); m != nil && !cur.havePos {
			cur.line, _ = strconv.Atoi(m[1])
			cur.col, _ = strconv.Atoi(m[2])
			cur.line--
			cur.col--
			cur.havePos = true
			continue
		}
		if m := taploLabel.FindStringSubmatch(l); m != nil && cur.label == "" {
			cur.label = m[1]
		}
	}
	flush()
	return out, nil
}
