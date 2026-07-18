package broker

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultSocketPath returns the default broker socket path:
//
//   - $XDG_RUNTIME_DIR/lspbridge/broker.sock when XDG_RUNTIME_DIR is set;
//   - otherwise <os.TempDir>/lspbridge-<uid>/broker.sock.
//
// The parent directory is not created here — [Listen] creates it (0700).
func DefaultSocketPath() string {
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return filepath.Join(rt, "lspbridge", "broker.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("lspbridge-%d", os.Getuid()), "broker.sock")
}

// Listen creates a path-based unix-domain listener at path. It creates the
// parent directory (0700) and clears a stale socket file left by a previous
// crash, but refuses to steal a socket a live broker is still listening on.
//
// Abstract-namespace sockets are rejected (a leading "@" or NUL): per ADR-0013
// §Gap 4 they ignore the mount namespace and would defeat the path-as-grant
// reachability boundary, so the broker is path-based only.
func Listen(path string) (net.Listener, error) {
	if path == "" {
		return nil, fmt.Errorf("broker: empty socket path")
	}
	if strings.HasPrefix(path, "@") || strings.HasPrefix(path, "\x00") {
		return nil, fmt.Errorf("broker: abstract sockets are forbidden (%q); use a filesystem path", path)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("broker: mkdir %s: %w", dir, err)
	}
	if err := clearStaleSocket(path); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("broker: listen %s: %w", path, err)
	}
	return ln, nil
}

// clearStaleSocket removes a leftover socket file at path if no broker is live
// behind it. It errors if a non-socket file occupies the path, or if a broker
// is still listening there (probed by a short dial).
func clearStaleSocket(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("broker: stat %s: %w", path, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("broker: %s exists and is not a socket; refusing to remove", path)
	}
	if c, derr := net.DialTimeout("unix", path, 200*time.Millisecond); derr == nil {
		_ = c.Close()
		return fmt.Errorf("broker: another broker is already listening on %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("broker: remove stale socket %s: %w", path, err)
	}
	return nil
}
