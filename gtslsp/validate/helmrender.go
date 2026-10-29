package validate

import (
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// chunk is one rendered manifest document and the template it came from.
type chunk struct {
	source string // "templates/x.yaml" (chart-name prefix removed)
	node   *yaml.Node
	kind   string
	name   string
}

var sourceRe = regexp.MustCompile(`(?m)^# Source: (\S+)`)

// splitRendered splits `helm template` output on document separators, in
// document order, skipping empty documents.
func splitRendered(s string) []chunk {
	var out []chunk
	for _, part := range regexp.MustCompile(`(?m)^---\s*$`).Split(s, -1) {
		if strings.TrimSpace(part) == "" {
			continue
		}
		docs := yamlDocs([]byte(part))
		if len(docs) == 0 || docRoot(docs[0]) == nil || docRoot(docs[0]).Kind != yaml.MappingNode {
			continue
		}
		c := chunk{node: docs[0], kind: docKind(docs[0]), name: docName(docs[0])}
		if m := sourceRe.FindStringSubmatch(part); m != nil {
			src := m[1]
			if i := strings.Index(src, "/"); i >= 0 {
				src = src[i+1:] // drop the chart-name prefix
			}
			c.source = src
		}
		out = append(out, c)
	}
	return out
}

func matchChunk(cs []chunk, r kcResource, idx int) *chunk {
	for i := range cs {
		if cs[i].kind == r.Kind && cs[i].name == r.Name {
			return &cs[i]
		}
	}
	if idx < len(cs) {
		return &cs[idx]
	}
	return nil
}

// templateLine finds the template line that most likely produced the rendered
// field at segs: a line declaring the last non-numeric key, preferring one
// that contains a template action. Line 0 when nothing matches.
func templateLine(lines []string, segs []string) int {
	key := ""
	for i := len(segs) - 1; i >= 0; i-- {
		if _, err := strconv.Atoi(segs[i]); err != nil {
			key = segs[i]
			break
		}
	}
	if key == "" {
		return 0
	}
	re := regexp.MustCompile(`^\s*(?:-\s+)?["']?` + regexp.QuoteMeta(key) + `["']?\s*:`)
	first := -1
	for i, l := range lines {
		if !re.MatchString(l) {
			continue
		}
		if strings.Contains(l, "{{") {
			return i
		}
		if first < 0 {
			first = i
		}
	}
	if first < 0 {
		return 0
	}
	return first
}
