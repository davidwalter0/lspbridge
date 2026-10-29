package validate

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// yamlDocs parses every document in text (JSON parses as YAML). Parsing stops
// at the first syntax error; the documents decoded so far are returned.
func yamlDocs(text []byte) []*yaml.Node {
	dec := yaml.NewDecoder(bytes.NewReader(text))
	var out []*yaml.Node
	for {
		var n yaml.Node
		if err := dec.Decode(&n); err != nil {
			if errors.Is(err, io.EOF) {
				return out
			}
			return out
		}
		out = append(out, &n)
	}
}

func docRoot(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return n
}

func mapGet(m *yaml.Node, key string) (k, v *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

// locate resolves segs below the document root and returns the 0-based line
// and column of the deepest node it could reach (the key node for mapping
// entries). found reports whether every segment resolved.
func locate(doc *yaml.Node, segs []string) (line, col int, found bool) {
	cur := docRoot(doc)
	if cur == nil {
		return 0, 0, false
	}
	pos := cur
	for _, s := range segs {
		switch cur.Kind {
		case yaml.MappingNode:
			k, v := mapGet(cur, s)
			if k == nil {
				return pos.Line - 1, pos.Column - 1, false
			}
			pos, cur = k, v
		case yaml.SequenceNode:
			i, err := strconv.Atoi(s)
			if err != nil || i < 0 || i >= len(cur.Content) {
				return pos.Line - 1, pos.Column - 1, false
			}
			pos, cur = cur.Content[i], cur.Content[i]
		case yaml.AliasNode:
			if cur.Alias == nil {
				return pos.Line - 1, pos.Column - 1, false
			}
			return pos.Line - 1, pos.Column - 1, false
		default:
			return pos.Line - 1, pos.Column - 1, false
		}
	}
	return pos.Line - 1, pos.Column - 1, true
}

// childKey reports whether the mapping at segs has a key named name, returning
// segs+name when it does.
func childKey(doc *yaml.Node, segs []string, name string) ([]string, bool) {
	cur := docRoot(doc)
	for _, s := range segs {
		if cur == nil {
			return nil, false
		}
		switch cur.Kind {
		case yaml.MappingNode:
			_, cur = mapGet(cur, s)
		case yaml.SequenceNode:
			i, err := strconv.Atoi(s)
			if err != nil || i < 0 || i >= len(cur.Content) {
				return nil, false
			}
			cur = cur.Content[i]
		default:
			return nil, false
		}
	}
	if k, _ := mapGet(cur, name); k != nil {
		return append(append([]string{}, segs...), name), true
	}
	return nil, false
}

var jsonPathTok = regexp.MustCompile(`\["([^"]+)"\]|\.\"([^"]+)\"|\[(\d+)\]|\.([^.\[]+)`)

// segsFromJSONPath parses check-jsonschema's "$.a.b[0]" paths.
func segsFromJSONPath(p string) []string {
	p = strings.TrimPrefix(p, "$")
	var out []string
	for _, m := range jsonPathTok.FindAllStringSubmatch(p, -1) {
		for _, g := range m[1:] {
			if g != "" {
				out = append(out, g)
				break
			}
		}
	}
	return out
}

// segsFromPointer parses an RFC 6901 JSON pointer ("/spec/replicas").
func segsFromPointer(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	for i, s := range parts {
		s = strings.ReplaceAll(s, "~1", "/")
		parts[i] = strings.ReplaceAll(s, "~0", "~")
	}
	return parts
}

var extraKeyRe = regexp.MustCompile(`(?:additional properties|unexpected|was unexpected|Additional properties are not allowed)[^']*'([^']+)'`)

// refineExtra moves a location onto the offending key when a schema message
// names an additional/unexpected property ("additional properties 'x' not
// allowed", "('x' was unexpected)").
func refineExtra(doc *yaml.Node, segs []string, msg string) []string {
	if m := extraKeyRe.FindStringSubmatch(msg); m != nil {
		if s, ok := childKey(doc, segs, m[1]); ok {
			return s
		}
	}
	return segs
}
