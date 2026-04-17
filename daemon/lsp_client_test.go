package daemon

import (
	"testing"
)

func TestParseClientArgs_valid(t *testing.T) {
	args := []string{"--socket", "/tmp/ohm.sock", "--root", "/srv/proj", "--lang", "go", "--", "gopls", "-v"}
	socket, root, lang, cmd, err := parseClientArgs(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if socket != "/tmp/ohm.sock" {
		t.Errorf("socket: got %q", socket)
	}
	if root != "/srv/proj" {
		t.Errorf("root: got %q", root)
	}
	if lang != "go" {
		t.Errorf("lang: got %q", lang)
	}
	if len(cmd) != 2 || cmd[0] != "gopls" || cmd[1] != "-v" {
		t.Errorf("cmd: got %v", cmd)
	}
}

func TestParseClientArgs_differentOrder(t *testing.T) {
	args := []string{"--lang", "rust", "--root", "/ws", "--socket", "/var/ohm.sock", "--", "rust-analyzer"}
	socket, root, lang, cmd, err := parseClientArgs(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if socket != "/var/ohm.sock" {
		t.Errorf("socket: got %q", socket)
	}
	if root != "/ws" {
		t.Errorf("root: got %q", root)
	}
	if lang != "rust" {
		t.Errorf("lang: got %q", lang)
	}
	if len(cmd) != 1 || cmd[0] != "rust-analyzer" {
		t.Errorf("cmd: got %v", cmd)
	}
}

func TestParseClientArgs_noCmd(t *testing.T) {
	// -- present but no command after it
	args := []string{"--socket", "/s", "--root", "/r", "--lang", "go", "--"}
	_, _, _, cmd, err := parseClientArgs(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd) != 0 {
		t.Errorf("expected empty cmd, got %v", cmd)
	}
}

func TestParseClientArgs_missingSocket(t *testing.T) {
	args := []string{"--root", "/r", "--lang", "go", "--", "gopls"}
	_, _, _, _, err := parseClientArgs(args)
	if err == nil {
		t.Fatal("expected error for missing --socket")
	}
}

func TestParseClientArgs_missingRoot(t *testing.T) {
	args := []string{"--socket", "/s", "--lang", "go", "--", "gopls"}
	_, _, _, _, err := parseClientArgs(args)
	if err == nil {
		t.Fatal("expected error for missing --root")
	}
}

func TestParseClientArgs_missingLang(t *testing.T) {
	args := []string{"--socket", "/s", "--root", "/r", "--", "gopls"}
	_, _, _, _, err := parseClientArgs(args)
	if err == nil {
		t.Fatal("expected error for missing --lang")
	}
}

func TestParseClientArgs_missingDoubleDash(t *testing.T) {
	args := []string{"--socket", "/s", "--root", "/r", "--lang", "go"}
	_, _, _, _, err := parseClientArgs(args)
	if err == nil {
		t.Fatal("expected error for missing -- separator")
	}
}

func TestParseClientArgs_socketMissingValue(t *testing.T) {
	args := []string{"--socket"}
	_, _, _, _, err := parseClientArgs(args)
	if err == nil {
		t.Fatal("expected error when --socket has no value")
	}
}
