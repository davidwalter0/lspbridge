package jsonrpc

import "io"

// stream adapts a separate reader and writer (e.g. a subprocess's stdout and
// stdin) into a single io.ReadWriteCloser suitable for [NewConn].
type stream struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (s stream) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s stream) Write(p []byte) (int, error) { return s.w.Write(p) }

// Close closes both halves, returning the first error encountered.
func (s stream) Close() error {
	errR := s.r.Close()
	errW := s.w.Close()
	if errR != nil {
		return errR
	}
	return errW
}

// Join adapts a reader and a writer into one [io.ReadWriteCloser]. The typical
// use is a language-server subprocess: Join(cmd.Stdout, cmd.Stdin), reading the
// server's output and writing to its input.
func Join(r io.ReadCloser, w io.WriteCloser) io.ReadWriteCloser {
	return stream{r: r, w: w}
}
