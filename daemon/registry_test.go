package daemon

import (
	"testing"
	"time"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	key := ServerKey{RootDir: "/tmp/proj", LanguageID: "go"}
	server := &LSPServer{PID: 1234, Refs: 1, LastResponse: time.Now()}

	r.Register(key, server)

	got, ok := r.Get(key)
	if !ok {
		t.Fatal("expected server, got nothing")
	}
	if got.PID != 1234 {
		t.Errorf("PID: want 1234, got %d", got.PID)
	}
}

func TestRegistry_Remove(t *testing.T) {
	r := NewRegistry()
	key := ServerKey{RootDir: "/tmp/proj", LanguageID: "go"}
	r.Register(key, &LSPServer{PID: 1})

	r.Remove(key)

	_, ok := r.Get(key)
	if ok {
		t.Fatal("expected server to be removed")
	}
}

func TestRegistry_IncrDecrRef(t *testing.T) {
	r := NewRegistry()
	key := ServerKey{RootDir: "/tmp/proj", LanguageID: "go"}
	r.Register(key, &LSPServer{PID: 1, Refs: 1})

	r.IncrRef(key)
	s, _ := r.Get(key)
	if s.Refs != 2 {
		t.Errorf("after IncrRef: want 2, got %d", s.Refs)
	}

	refs := r.DecrRef(key)
	if refs != 1 {
		t.Errorf("DecrRef return: want 1, got %d", refs)
	}
}

func TestRegistry_MissingKey(t *testing.T) {
	r := NewRegistry()
	key := ServerKey{RootDir: "/nope", LanguageID: "rust"}

	_, ok := r.Get(key)
	if ok {
		t.Fatal("expected miss, got hit")
	}

	refs := r.DecrRef(key)
	if refs != 0 {
		t.Errorf("DecrRef on missing key: want 0, got %d", refs)
	}
}
