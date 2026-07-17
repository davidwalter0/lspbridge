package jsonrpc

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// writeFrame writes one Content-Length-framed message: the header block
// terminated by a blank line, followed by the raw payload bytes.
func writeFrame(w io.Writer, payload []byte) error {
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// readFrame reads one Content-Length-framed message body from r. It parses
// the header block (only Content-Length is significant), then reads exactly
// that many payload bytes. A missing or malformed Content-Length is an error.
func readFrame(r *bufio.Reader) ([]byte, error) {
	contentLen := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // blank line terminates the header block
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("jsonrpc: malformed header line %q", line)
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("jsonrpc: invalid Content-Length %q: %w", value, err)
			}
			if n < 0 {
				return nil, fmt.Errorf("jsonrpc: negative Content-Length %d", n)
			}
			contentLen = n
		}
	}
	if contentLen < 0 {
		return nil, fmt.Errorf("jsonrpc: message missing Content-Length header")
	}
	buf := make([]byte, contentLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
