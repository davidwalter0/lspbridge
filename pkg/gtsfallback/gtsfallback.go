// Package gtsfallback wires the gts-lsp binary into the broker as the server
// of last resort: it asks the binary which language claims a file and which
// languages it can serve, so the zero-dependency root module never has to link
// a grammar registry.
//
// Both questions are answered by short-lived gts-lsp invocations
// (`--detect PATH`, `--languages`) and cached for the process lifetime.
package gtsfallback

import (
	"context"
	"encoding/json"
	"os/exec"
	"sync"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/broker"
	"github.com/davidwalter0/lspbridge/pkg/projectcontext"
	"github.com/davidwalter0/lspbridge/pkg/server"
)

const probeTimeout = 15 * time.Second

// aliases maps a languageId a dedicated resolver stamps to the gts grammar
// name that serves the same language.
var aliases = map[string]string{"shellscript": "bash"}

// Fallback is the gts-lsp-backed resolver and spec source. The zero value
// resolves "gts-lsp" through PATH; Binary overrides that (tests).
type Fallback struct {
	// Binary is the gts-lsp path; empty means exec.LookPath("gts-lsp").
	Binary string

	once    sync.Once
	bin     string // resolved path, "" if absent
	mu      sync.Mutex
	detect  map[string]detected
	langs   map[string]bool
	langsOK bool
}

type detected struct {
	lang string
	ok   bool
}

func (f *Fallback) binary() string {
	f.once.Do(func() {
		if f.Binary != "" {
			f.bin = f.Binary
			return
		}
		if p, err := exec.LookPath(server.GtsLspCommand); err == nil {
			f.bin = p
		}
	})
	return f.bin
}

// Available reports whether a gts-lsp binary was found.
func (f *Fallback) Available() bool { return f.binary() != "" }

func (f *Fallback) run(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	return exec.CommandContext(ctx, f.binary(), args...).Output()
}

// Detect reports the language gts-lsp assigns to absPath. It declines when the
// binary is absent or claims nothing for the file.
func (f *Fallback) Detect(absPath string) (string, bool) {
	if !f.Available() {
		return "", false
	}
	f.mu.Lock()
	if d, hit := f.detect[absPath]; hit {
		f.mu.Unlock()
		return d.lang, d.ok
	}
	f.mu.Unlock()
	out, err := f.run("--detect", absPath)
	d := detected{}
	if err == nil {
		if name := firstLine(out); name != "" {
			d = detected{lang: name, ok: true}
		}
	}
	f.mu.Lock()
	if f.detect == nil {
		f.detect = map[string]detected{}
	}
	f.detect[absPath] = d
	f.mu.Unlock()
	return d.lang, d.ok
}

func firstLine(b []byte) string {
	for i, c := range b {
		if c == '\n' || c == '\r' {
			b = b[:i]
			break
		}
	}
	return string(b)
}

// Supports reports whether gts-lsp has a grammar for the languageId (after
// aliasing). The set is fetched once.
func (f *Fallback) Supports(language string) bool {
	if !f.Available() {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.langsOK {
		f.langsOK = true
		if out, err := f.run("--languages"); err == nil {
			var names []string
			if json.Unmarshal(out, &names) == nil {
				f.langs = make(map[string]bool, len(names))
				for _, n := range names {
					f.langs[n] = true
				}
			}
		}
	}
	if a, ok := aliases[language]; ok {
		language = a
	}
	return f.langs[language]
}

// Resolver is the catch-all resolver to append after the dedicated chain.
func (f *Fallback) Resolver() projectcontext.Resolver {
	return projectcontext.FallbackResolver{Detect: f.Detect}
}

// Chain is DefaultChain followed by the gts-lsp catch-all.
func (f *Fallback) Chain() projectcontext.Resolver {
	c := append(projectcontext.Chain{}, projectcontext.DefaultChain()...)
	return append(c, f.Resolver())
}

// SpecFor wraps a dedicated SpecFunc: the dedicated server wins, and gts-lsp
// serves only when the dedicated lookup fails (no preset, or its binary is
// missing) AND gts-lsp has a grammar for the language. Otherwise the original
// error is returned unchanged, so an unclaimed language keeps the broker's
// "no language server configured" text.
func (f *Fallback) SpecFor(dedicated broker.SpecFunc) broker.SpecFunc {
	if dedicated == nil {
		dedicated = broker.DefaultSpecFor
	}
	return func(pc projectcontext.Context) (server.Spec, error) {
		spec, err := dedicated(pc)
		if err == nil || !f.Supports(pc.Language) {
			return spec, err
		}
		gts, gerr := server.GtsLspSpec(pc.Root)
		if gerr != nil {
			return server.Spec{}, err
		}
		return gts, nil
	}
}
