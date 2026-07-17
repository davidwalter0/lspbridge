package jsonrpc

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{"empty", ""},
		{"small", `{"jsonrpc":"2.0","id":1}`},
		{"utf8", `{"msg":"héllo — 世界"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeFrame(&buf, []byte(tt.payload)); err != nil {
				t.Fatalf("writeFrame: %v", err)
			}
			got, err := readFrame(bufio.NewReader(&buf))
			if err != nil {
				t.Fatalf("readFrame: %v", err)
			}
			if string(got) != tt.payload {
				t.Errorf("round trip = %q, want %q", got, tt.payload)
			}
		})
	}
}

func TestReadFrameErrors(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"missing content-length", "X-Foo: bar\r\n\r\nbody"},
		{"malformed header", "no-colon-here\r\n\r\n"},
		{"bad content-length", "Content-Length: notanumber\r\n\r\n"},
		{"negative content-length", "Content-Length: -5\r\n\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := readFrame(bufio.NewReader(strings.NewReader(tt.raw)))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestReadFrameContentLengthCaseInsensitive(t *testing.T) {
	raw := "content-length: 2\r\n\r\nhi"
	got, err := readFrame(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if string(got) != "hi" {
		t.Errorf("got %q, want %q", got, "hi")
	}
}
