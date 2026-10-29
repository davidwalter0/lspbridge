package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/davidwalter0/lspbridge/gtslsp/validate"
)

// schemasCmd implements `gts-lsp schemas sync`. It is the only network-using
// path and runs only when asked for explicitly; the language server never
// fetches. Returns the process exit code.
func schemasCmd(args []string) int {
	if len(args) == 0 || args[0] != "sync" {
		fmt.Fprintln(os.Stderr, "usage: gts-lsp schemas sync [--k8s-version V] [--kinds a,b,...] [URL...]")
		return 2
	}
	fs := flag.NewFlagSet("schemas sync", flag.ContinueOnError)
	ver := fs.String("k8s-version", "", "Kubernetes schema version (default: $GTS_LSP_K8S_VERSION or the built-in)")
	kinds := fs.String("kinds", "", "comma-separated kinds (default set if empty; Kind=apiVersion for others)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	var urls []string
	extra := fs.Args()
	// With explicit URLs and no --kinds, sync only those URLs; otherwise the
	// Kubernetes set (default kinds, or the --kinds given) plus the URLs.
	if *kinds != "" || len(extra) == 0 {
		var ks []string
		if *kinds != "" {
			ks = strings.Split(*kinds, ",")
		}
		k, err := validate.K8sSchemaURLs(*ver, ks)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gts-lsp:", err)
			return 2
		}
		urls = append(urls, k...)
	}
	urls = append(urls, extra...)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	res, err := validate.SyncWithReport(ctx, urls)
	var total int64
	for _, r := range res {
		fmt.Printf("fetched %s -> %s (%d bytes)\n", r.URL, r.Path, r.Bytes)
		total += r.Bytes
	}
	fmt.Printf("%d schemas, %d bytes in %s\n", len(res), total, validate.CacheDir())
	if err != nil {
		fmt.Fprintln(os.Stderr, "gts-lsp:", err)
		return 1
	}
	return 0
}
