package daemon

import (
	"sync"
	"time"
)

type ServerKey struct {
	RootDir    string
	LanguageID string
}

type LSPServer struct {
	PID          int
	Refs         int
	Process      *Process
	LastResponse time.Time
}

type Registry struct {
	mu      sync.Mutex
	servers map[ServerKey]*LSPServer
}

func NewRegistry() *Registry {
	return &Registry{
		servers: make(map[ServerKey]*LSPServer),
	}
}

func (r *Registry) Get(key ServerKey) (*LSPServer, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.servers[key]
	return s, ok
}

func (r *Registry) Register(key ServerKey, server *LSPServer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.servers[key] = server
}

func (r *Registry) Remove(key ServerKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.servers, key)
}
