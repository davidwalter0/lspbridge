// Package server launches a language-server subprocess and connects its stdio
// to an LSP client. It is the reusable transport the broker uses to spin up a
// warm session for any server (pyright, tsserver, rust-analyzer, …): only the
// [Spec] differs per language.
package server

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/davidwalter0/lspbridge/pkg/jsonrpc"
	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// Spec describes how to launch a language server over stdio.
type Spec struct {
	// Command is the server executable (e.g. "pyright-langserver").
	Command string
	// Args are its arguments (e.g. []string{"--stdio"}).
	Args []string
	// Dir is the working directory for the process (typically the project
	// root); empty means inherit the parent's.
	Dir string
	// Stderr, if non-nil, receives the server's standard error. A nil Stderr
	// discards it.
	Stderr io.Writer
}

// Server is a running language-server subprocess plus its LSP client.
type Server struct {
	cmd    *exec.Cmd
	conn   *jsonrpc.Conn
	client *lsp.Client
}

// Launch starts the server described by spec and connects its stdio to a new
// LSP client. handler receives any server-to-client requests/notifications and
// may be nil. The process is running on return; the caller drives the LSP
// handshake via Client().Initialize and must call Close (or Shutdown) to
// release it.
func Launch(ctx context.Context, spec Spec, handler jsonrpc.Handler) (*Server, error) {
	if spec.Command == "" {
		return nil, fmt.Errorf("server: empty command")
	}
	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Stderr = spec.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("server: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("server: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("server: start %q: %w", spec.Command, err)
	}

	conn := jsonrpc.NewConn(jsonrpc.Join(stdout, stdin), handler)
	return &Server{
		cmd:    cmd,
		conn:   conn,
		client: lsp.NewClient(conn),
	}, nil
}

// Client returns the LSP client bound to this server.
func (s *Server) Client() *lsp.Client { return s.client }

// Conn returns the underlying JSON-RPC connection.
func (s *Server) Conn() *jsonrpc.Conn { return s.conn }

// Shutdown performs an orderly LSP shutdown/exit, then closes the connection
// and waits for the process to terminate.
func (s *Server) Shutdown(ctx context.Context) error {
	shutdownErr := s.client.Shutdown(ctx)
	closeErr := s.conn.Close()
	waitErr := s.cmd.Wait()
	// Report the most meaningful error: a failed shutdown handshake first,
	// then wait status (Close of a subprocess pipe routinely reports the peer
	// already gone, which is expected here).
	if shutdownErr != nil {
		return shutdownErr
	}
	if waitErr != nil {
		return waitErr
	}
	return closeErr
}

// Close force-terminates the server without an orderly LSP shutdown: it closes
// the connection (killing the process via the command context on cancel) and
// waits. Use when the handshake state is unknown or shutdown failed.
func (s *Server) Close() error {
	_ = s.conn.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
	return nil
}
