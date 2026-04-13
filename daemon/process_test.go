package daemon

import (
	"strings"
	"testing"
)

func TestSendNotification_Frame(t *testing.T) {
	var buf strings.Builder

	// fake a WriteCloser backed by the string builder
	proc := &Process{Stdin: &nopWriteCloser{&buf}}

	err := proc.SendNotification("textDocument/didClose", map[string]interface{}{
		"textDocument": map[string]string{"uri": "file:///foo/bar.go"},
	})
	if err != nil {
		t.Fatalf("SendNotification: %v", err)
	}

	out := buf.String()
	if !strings.HasPrefix(out, "Content-Length:") {
		t.Errorf("missing Content-Length header, got: %s", out)
	}
	if !strings.Contains(out, "textDocument/didClose") {
		t.Errorf("missing method in body, got: %s", out)
	}
}

// nopWriteCloser wraps a strings.Builder so it satisfies io.WriteCloser.
type nopWriteCloser struct {
	*strings.Builder
}

func (n *nopWriteCloser) Close() error { return nil }
