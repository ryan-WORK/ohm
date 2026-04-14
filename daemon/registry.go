package daemon

import (
	"net"
	"os"
	"sync"
)

type ServerKey struct {
	RootDir    string
	LanguageID string
}

type LSPServer struct {
	PID         int
	Refs        int
	Process     *Process
	Command     string
	Args        []string
	ProxySocket string
	proxyLn     net.Listener

	mu  sync.RWMutex
	mux *Mux
}

func (s *LSPServer) GetMux() *Mux {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mux
}

func (s *LSPServer) SetMux(m *Mux) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mux = m
}

// Close shuts down the LSP server gracefully, then removes the proxy socket.
func (s *LSPServer) Close() {
	if s.proxyLn != nil {
		s.proxyLn.Close()
	}
	s.GetMux().GracefulShutdown()
	if s.ProxySocket != "" {
		os.Remove(s.ProxySocket)
	}
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

func (r *Registry) IncrRef(key ServerKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.servers[key]; ok {
		s.Refs++
	}
}

func (r *Registry) DecrRef(key ServerKey) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.servers[key]; ok {
		s.Refs--
		return s.Refs
	}
	return 0
}
