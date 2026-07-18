package broker

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultSocketPath(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if got := DefaultSocketPath(); got != "/run/user/1000/lspbridge/broker.sock" {
		t.Fatalf("with XDG_RUNTIME_DIR: %q", got)
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	got := DefaultSocketPath()
	if !strings.Contains(got, "lspbridge-") || !strings.HasSuffix(got, "broker.sock") {
		t.Fatalf("fallback path: %q", got)
	}
}

func TestListenRejectsAbstractAndEmpty(t *testing.T) {
	if _, err := Listen(""); err == nil {
		t.Error("empty path should error")
	}
	if _, err := Listen("@abstract"); err == nil {
		t.Error("abstract socket (@) should be rejected")
	}
	if _, err := Listen("\x00abstract"); err == nil {
		t.Error("abstract socket (NUL) should be rejected")
	}
}

func TestListenClearsStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale.sock")

	// Create a socket file, then leave it behind WITHOUT unlinking (simulating a
	// crashed broker: the file persists but nothing listens).
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("prime listener: %v", err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stale socket should persist: %v", err)
	}

	// Listen must detect the stale socket, remove it, and bind.
	ln2, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen over stale socket: %v", err)
	}
	_ = ln2.Close()
}

func TestListenRefusesNonSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "regular.file")
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := Listen(path); err == nil {
		t.Error("Listen over a regular file should refuse")
	}
}

func TestListenRefusesLiveSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("first Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	// A second Listen while the first is live must refuse rather than steal it.
	if _, err := Listen(path); err == nil {
		t.Error("Listen over a live socket should refuse")
	}
}
