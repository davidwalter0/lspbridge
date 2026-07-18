// Command lspbroker is the socket-activated LSP broker daemon (ADR-0013
// pattern). It listens on a path-based unix socket, lazily spawns one warm
// language-server session per (projectRoot, language) on first "broker/query",
// idle-reaps quiescent sessions, and shuts every session down cleanly on
// SIGTERM/SIGINT or a "broker/shutdown" request.
//
// Flags use the standard-library flag package on purpose: lspbridge is a
// pure-standard-library, zero-external-dependency, CGO_ENABLED=0 module (the
// keystone invariant of the semantic tier — see README.org and
// docs/design/wsedit-ae-seam.org), so the daemon must not pull in a flag
// library.
//
// Usage:
//
//	lspbroker [--socket PATH] [--idle-timeout DUR] [--verbose]
package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/davidwalter0/lspbridge/pkg/broker"
)

func main() {
	socket := flag.String("socket", "", "unix socket path (default: $XDG_RUNTIME_DIR/lspbridge/broker.sock, else <tmp>/lspbridge-<uid>/broker.sock)")
	idle := flag.Duration("idle-timeout", broker.DefaultIdleTimeout, "shut down a warm session after this much quiescence")
	verbose := flag.Bool("verbose", false, "forward spawned language-server stderr to this process's stderr")
	flag.Parse()

	path := *socket
	if path == "" {
		path = broker.DefaultSocketPath()
	}

	var serverStderr io.Writer
	if *verbose {
		serverStderr = os.Stderr
	}

	b := broker.New(broker.Config{
		IdleTimeout:  *idle,
		ServerStderr: serverStderr,
	})

	ln, err := broker.Listen(path)
	if err != nil {
		log.Fatalf("lspbroker: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// broker/shutdown cancels the serve context, unblocking Serve just like a
	// signal would.
	h := broker.NewHandler(b, stop)

	log.Printf("lspbroker: listening on %s (idle-timeout %s)", path, idle.String())
	serveErr := broker.Serve(ctx, ln, h)

	// Serve has returned (signal, shutdown request, or accept error): tear down
	// every warm session and remove the socket file.
	b.ShutdownAll()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("lspbroker: remove socket %s: %v", path, err)
	}
	if serveErr != nil {
		log.Fatalf("lspbroker: serve: %v", serveErr)
	}
	log.Printf("lspbroker: stopped")
}
