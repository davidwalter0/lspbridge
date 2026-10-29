package gts

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/davidwalter0/gotreesitter"
	"github.com/davidwalter0/gotreesitter/grammars"
)

// parseTimeoutMicros bounds one parse; a stopped parse is reported as a
// diagnostic instead of hanging the server.
const parseTimeoutMicros = 10_000_000

// Lang is one loaded grammar plus the lazily built query engines over it.
//
// A Lang is NOT safe for concurrent use: the parser, highlighter, tagger and
// outliner each keep scratch buffers, and an incremental reparse mutates the
// previous tree in place (Tree.Edit). Every use of a Lang, and of any tree
// parsed with it, happens under mu.
type Lang struct {
	Name  string
	Entry grammars.LangEntry

	mu       sync.Mutex
	lang     *gotreesitter.Language
	loadErr  error
	loaded   bool
	parser   *gotreesitter.Parser
	hl       *gotreesitter.Highlighter
	hlErr    error
	hlDone   bool
	tagger   *gotreesitter.Tagger
	outliner *gotreesitter.Outliner
	tagsDone bool
	tagsErr  error
	keywords []string
	kwDone   bool
}

var (
	langsMu sync.Mutex
	langs   = map[string]*Lang{}
)

// LangByName returns the shared Lang for a registered grammar name (or a
// linguist alias such as "golang"), or nil.
func LangByName(name string) *Lang {
	e := grammars.DetectLanguageByName(name)
	if e == nil {
		return nil
	}
	return langFor(*e)
}

func langFor(e grammars.LangEntry) *Lang {
	langsMu.Lock()
	defer langsMu.Unlock()
	if l := langs[e.Name]; l != nil {
		return l
	}
	l := &Lang{Name: e.Name, Entry: e}
	langs[e.Name] = l
	return l
}

// Hooks lets a build splice in language knowledge gts itself lacks (for
// example Helm templates). Both fields are optional.
type Hooks struct {
	// DetectLanguage may claim a document before the generic detection runs.
	// It returns a registered grammar name, or "" to decline.
	DetectLanguage func(path string, src []byte) string
	// ParseView returns the bytes to hand the parser for a document of the
	// given language. It MUST preserve every byte offset (same length, same
	// newlines); a view of a different length is ignored.
	ParseView func(path, lang string, src []byte) []byte
}

// Detect picks the grammar for a document: hook, then filename (with the C/C++
// header content sniff), then shebang, then the client's languageId.
func (h Hooks) Detect(path, languageID string, src []byte) *Lang {
	if h.DetectLanguage != nil {
		if name := h.DetectLanguage(path, src); name != "" {
			if l := LangByName(name); l != nil {
				return l
			}
		}
	}
	if e := grammars.DetectLanguageWithContent(path, src); e != nil {
		return langFor(*e)
	}
	if len(src) > 2 && src[0] == '#' && src[1] == '!' {
		line := src
		if i := indexByte(src, '\n'); i >= 0 {
			line = src[:i]
		}
		if e := grammars.DetectLanguageByShebang(strings.TrimRight(string(line), "\r")); e != nil {
			return langFor(*e)
		}
	}
	if languageID != "" {
		return LangByName(languageID)
	}
	return nil
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// load resolves the grammar. Caller holds mu.
func (l *Lang) load() error {
	if l.loaded {
		return l.loadErr
	}
	l.loaded = true
	defer func() {
		if r := recover(); r != nil {
			l.loadErr = fmt.Errorf("loading grammar %s: %v", l.Name, r)
		}
	}()
	l.lang = l.Entry.Language()
	if l.lang == nil {
		l.loadErr = fmt.Errorf("grammar %s did not load", l.Name)
		return l.loadErr
	}
	l.parser = gotreesitter.NewParser(l.lang)
	l.parser.SetTimeoutMicros(parseTimeoutMicros)
	return nil
}

func (l *Lang) tokenSourceFactory() gotreesitter.TokenSourceFactory {
	if l.Entry.TokenSourceFactory == nil {
		return nil
	}
	return func(src []byte) (gotreesitter.TokenSource, error) {
		return l.Entry.TokenSourceFactory(src, l.lang), nil
	}
}

// parse parses src, incrementally against old when it is non-nil. Caller
// holds mu. A partial tree (timeout, safety limit) comes back with stopped set.
func (l *Lang) parse(src []byte, old *gotreesitter.Tree) (tree *gotreesitter.Tree, stopped bool, err error) {
	if err := l.load(); err != nil {
		return nil, false, err
	}
	defer func() {
		if r := recover(); r != nil {
			tree, stopped, err = nil, false, fmt.Errorf("parse panic in %s: %v", l.Name, r)
		}
	}()
	factory := l.tokenSourceFactory()
	switch {
	case old != nil && factory != nil:
		tree, err = l.parser.ParseIncrementalWithTokenSourceFactoryStrict(src, old, factory)
	case old != nil:
		tree, err = l.parser.ParseIncrementalStrict(src, old)
	case factory != nil:
		tree, err = l.parser.ParseWithTokenSourceFactoryStrict(src, factory)
	default:
		tree, err = l.parser.ParseStrict(src)
	}
	if err != nil && tree != nil {
		return tree, true, nil
	}
	return tree, false, err
}

// highlighter builds (once) the highlighter. Caller holds mu.
func (l *Lang) highlighter() *gotreesitter.Highlighter {
	if l.hlDone {
		return l.hl
	}
	l.hlDone = true
	if l.load() != nil || strings.TrimSpace(l.Entry.HighlightQuery) == "" {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			l.hl, l.hlErr = nil, fmt.Errorf("highlight query panic: %v", r)
		}
	}()
	l.hl, l.hlErr = gotreesitter.NewHighlighter(l.lang, l.Entry.HighlightQuery)
	return l.hl
}

// tagsQuery returns the tags query in force and its source: "override" (a
// shipped queries/<lang>/tags.scm), "grammar" (gotreesitter's explicit or
// inferred query) or "none".
func (l *Lang) tagsQuery() (string, string) {
	if q, ok := overrideTagsQuery(l.Name); ok {
		return q, "override"
	}
	if q := grammars.ResolveTagsQuery(l.Entry); strings.TrimSpace(q) != "" {
		return q, "grammar"
	}
	return "", "none"
}

// tagEngines builds (once) the tagger and outliner from the tags query.
// Caller holds mu. Both are nil when the grammar has no usable tags query.
func (l *Lang) tagEngines() (*gotreesitter.Tagger, *gotreesitter.Outliner) {
	if l.tagsDone {
		return l.tagger, l.outliner
	}
	l.tagsDone = true
	if l.load() != nil {
		return nil, nil
	}
	defer func() {
		if r := recover(); r != nil {
			l.tagger, l.outliner, l.tagsErr = nil, nil, fmt.Errorf("tags query panic: %v", r)
		}
	}()
	q, _ := l.tagsQuery()
	if strings.TrimSpace(q) == "" {
		return nil, nil
	}
	tg, err := gotreesitter.NewTagger(l.lang, q)
	if err != nil {
		l.tagsErr = err
		return nil, nil
	}
	ol, err := gotreesitter.NewOutliner(l.lang, q)
	if err != nil {
		l.tagsErr = err
		return tg, nil
	}
	if ol.QueryEmpty() {
		ol = nil
	}
	l.tagger, l.outliner = tg, ol
	return l.tagger, l.outliner
}

var wordRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Keywords are the grammar's anonymous, word-shaped terminals ("if", "func",
// "return"...). Caller holds mu.
func (l *Lang) Keywords() []string {
	if l.kwDone {
		return l.keywords
	}
	l.kwDone = true
	if l.load() != nil {
		return nil
	}
	seen := map[string]bool{}
	for i, md := range l.lang.SymbolMetadata {
		if md.Named || !md.Visible || i >= len(l.lang.SymbolNames) {
			continue
		}
		name := l.lang.SymbolNames[i]
		if wordRE.MatchString(name) && !seen[name] {
			seen[name] = true
			l.keywords = append(l.keywords, name)
		}
	}
	sort.Strings(l.keywords)
	return l.keywords
}

// Features is what the server can answer for one language.
type Features struct {
	Diagnostics    bool `json:"diagnostics"`
	Symbols        bool `json:"documentSymbol"`
	Folding        bool `json:"foldingRange"`
	Selection      bool `json:"selectionRange"`
	SemanticTokens bool `json:"semanticTokens"`
	// Xref (definition/references by tag name) is a HEURISTIC.
	Xref bool `json:"definitionReferencesHeuristic"`
	// Completion (keywords + in-file identifiers) is a HEURISTIC.
	Completion bool `json:"completionHeuristic"`
	// TagsSource says where the tags query comes from: "override" (shipped
	// with gts-lsp), "grammar" (gotreesitter's), or "none".
	TagsSource string `json:"tagsSource"`
	TagsError  string `json:"tagsError,omitempty"`
	LoadError  string `json:"loadError,omitempty"`
}

// Features reports what this language supports. It loads the grammar and
// compiles its queries.
func (l *Lang) Features() Features {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.load(); err != nil {
		return Features{LoadError: err.Error()}
	}
	tg, ol := l.tagEngines()
	_, src := l.tagsQuery()
	f := Features{TagsSource: src,
		Diagnostics:    true,
		Symbols:        ol != nil,
		Folding:        true,
		Selection:      true,
		SemanticTokens: l.highlighter() != nil,
		Xref:           tg != nil,
		Completion:     len(l.Keywords()) > 0 || tg != nil,
	}
	if l.tagsErr != nil {
		f.TagsError = l.tagsErr.Error()
	}
	return f
}

// Capability is one row of the --capabilities report.
type Capability struct {
	Language string   `json:"language"`
	Features Features `json:"features"`
}

// Capabilities reports the features of every registered grammar, sorted by name.
func Capabilities() []Capability {
	all := grammars.AllLanguages()
	out := make([]Capability, 0, len(all))
	for _, e := range all {
		out = append(out, Capability{Language: e.Name, Features: langFor(e).Features()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Language < out[j].Language })
	return out
}
