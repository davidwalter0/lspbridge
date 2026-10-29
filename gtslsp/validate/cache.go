package validate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CacheDir is the offline schema cache: $XDG_CACHE_HOME/gts-lsp/schemas
// (default ~/.cache/gts-lsp/schemas). Validation only ever reads it.
func CacheDir() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.TempDir()
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "gts-lsp", "schemas")
}

// cachePathForURL maps a schema URL to its file in the cache. Kubernetes
// schemas from yannh/kubernetes-json-schema keep the directory layout
// kubeconform's -schema-location template expects
// (k8s/<version>-standalone[-strict]/<file>.json); everything else is keyed by
// the SHA-256 of the URL.
func cachePathForURL(raw string) string {
	root := CacheDir()
	if u, err := url.Parse(raw); err == nil && u.Host == "raw.githubusercontent.com" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i, p := range parts {
			if p == "kubernetes-json-schema" && len(parts) >= i+4 {
				// kubernetes-json-schema/<ref>/<dir>/<file>
				return filepath.Join(root, "k8s", parts[i+2], parts[len(parts)-1])
			}
		}
	}
	sum := sha256.Sum256([]byte(raw))
	return filepath.Join(root, hex.EncodeToString(sum[:])+".json")
}

// SyncSchemas is the only code path that touches the network: it downloads
// each http(s) URL into the offline cache so later validation can run
// without fetching. It reports every failure, not just the first.
func SyncSchemas(ctx context.Context, urls []string) error {
	var errs []error
	client := &http.Client{Timeout: 60 * time.Second}
	for _, raw := range urls {
		if err := syncOne(ctx, client, raw); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", raw, err))
		}
	}
	return errors.Join(errs...)
}

func syncOne(ctx context.Context, c *http.Client, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("not an http(s) URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	dst := cachePathForURL(raw)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".sync-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, 64<<20)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}
