package jsonrpc

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type readCloser struct {
	r        io.Reader
	closed   bool
	closeErr error
}

func (rc *readCloser) Read(p []byte) (int, error) { return rc.r.Read(p) }
func (rc *readCloser) Close() error               { rc.closed = true; return rc.closeErr }

type writeCloser struct {
	w        io.Writer
	closed   bool
	closeErr error
}

func (wc *writeCloser) Write(p []byte) (int, error) { return wc.w.Write(p) }
func (wc *writeCloser) Close() error                { wc.closed = true; return wc.closeErr }

func TestJoinReadWriteClose(t *testing.T) {
	src := &readCloser{r: bytes.NewReader([]byte("hello"))}
	var out bytes.Buffer
	dst := &writeCloser{w: &out}
	rwc := Join(src, dst)

	buf := make([]byte, 5)
	n, err := rwc.Read(buf)
	if err != nil || string(buf[:n]) != "hello" {
		t.Fatalf("Read = %q, %v", buf[:n], err)
	}
	if _, err := rwc.Write([]byte("world")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if out.String() != "world" {
		t.Errorf("written = %q, want %q", out.String(), "world")
	}
	if err := rwc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !src.closed || !dst.closed {
		t.Errorf("Close did not close both halves: r=%v w=%v", src.closed, dst.closed)
	}
}

func TestJoinCloseReturnsReaderErrorFirst(t *testing.T) {
	src := &readCloser{r: bytes.NewReader(nil), closeErr: errors.New("reader boom")}
	dst := &writeCloser{w: &bytes.Buffer{}, closeErr: errors.New("writer boom")}
	err := Join(src, dst).Close()
	if err == nil || err.Error() != "reader boom" {
		t.Fatalf("Close error = %v, want reader boom", err)
	}
	// Both halves must still be closed even when the reader errors.
	if !src.closed || !dst.closed {
		t.Errorf("both halves must close: r=%v w=%v", src.closed, dst.closed)
	}
}

func TestJoinCloseReturnsWriterErrorWhenReaderOK(t *testing.T) {
	src := &readCloser{r: bytes.NewReader(nil)}
	dst := &writeCloser{w: &bytes.Buffer{}, closeErr: errors.New("writer boom")}
	if err := Join(src, dst).Close(); err == nil || err.Error() != "writer boom" {
		t.Fatalf("Close error = %v, want writer boom", err)
	}
}
