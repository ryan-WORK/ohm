package daemon

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestWriteReadFrame_roundtrip(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","method":"initialized","params":{}}`)

	var buf bytes.Buffer
	if err := WriteFrame(&buf, body); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := ReadFrame(bufio.NewReader(&buf))
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("roundtrip mismatch\ngot:  %s\nwant: %s", got, body)
	}
}

func TestWriteFrame_header(t *testing.T) {
	body := []byte(`{}`)
	var buf bytes.Buffer
	if err := WriteFrame(&buf, body); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	s := buf.String()
	if !strings.HasPrefix(s, "Content-Length: 2\r\n\r\n") {
		t.Errorf("unexpected header: %q", s)
	}
}

func TestWriteReadFrame_empty(t *testing.T) {
	body := []byte{}
	var buf bytes.Buffer
	if err := WriteFrame(&buf, body); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	got, err := ReadFrame(bufio.NewReader(&buf))
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty body, got %q", got)
	}
}

func TestReadFrame_missingContentLength(t *testing.T) {
	// Headers with no Content-Length, then blank line
	raw := "X-Custom: foo\r\n\r\n"
	_, err := ReadFrame(bufio.NewReader(strings.NewReader(raw)))
	if err == nil {
		t.Fatal("expected error for missing Content-Length, got nil")
	}
	if !strings.Contains(err.Error(), "missing Content-Length") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReadFrame_truncatedBody(t *testing.T) {
	// Content-Length says 100 bytes but only 3 bytes follow
	raw := "Content-Length: 100\r\n\r\nabc"
	_, err := ReadFrame(bufio.NewReader(strings.NewReader(raw)))
	if err == nil {
		t.Fatal("expected error for truncated body, got nil")
	}
}

func TestReadFrame_multipleHeaders(t *testing.T) {
	// LSP spec allows extra headers before Content-Length
	body := []byte(`{"id":1}`)
	var buf bytes.Buffer
	buf.WriteString("Content-Type: application/vscode-jsonrpc; charset=utf-8\r\n")
	if err := WriteFrame(&buf, body); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	// Rewrite: put extra header before Content-Length in a fresh buffer
	combined := "Content-Type: application/vscode-jsonrpc\r\nContent-Length: 8\r\n\r\n{\"id\":1}"
	got, err := ReadFrame(bufio.NewReader(strings.NewReader(combined)))
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if string(got) != `{"id":1}` {
		t.Errorf("got %q", got)
	}
}

func TestWriteReadFrame_multipleMessages(t *testing.T) {
	msgs := [][]byte{
		[]byte(`{"id":1,"method":"initialize"}`),
		[]byte(`{"id":2,"method":"shutdown"}`),
		[]byte(`{"method":"exit"}`),
	}

	var buf bytes.Buffer
	for _, m := range msgs {
		if err := WriteFrame(&buf, m); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
	}

	r := bufio.NewReader(&buf)
	for i, want := range msgs {
		got, err := ReadFrame(r)
		if err != nil {
			t.Fatalf("msg %d: ReadFrame: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("msg %d: got %s, want %s", i, got, want)
		}
	}
}
