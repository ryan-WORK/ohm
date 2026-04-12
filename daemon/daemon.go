package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

type Daemon struct {
	registry    *Registry
	pendingKill map[ServerKey]*time.Timer
	mu          sync.Mutex
}

type DetachMsg struct {
	RootDir    string `json:"root_dir"`
	LanguageID string `json:"language_id"`
}

type Msg struct {
	Type string `json:"type"` // "attach" or "detach"
}

type AttachMsg struct {
	RootDir    string `json:"root_dir"`
	LanguageID string `json:"language_id"`
	PID        int    `json:"pid"`
}

func Start(socketPath string) error {
	os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer ln.Close()

	d := &Daemon{
		registry:    NewRegistry(),
		pendingKill: make(map[ServerKey]*time.Timer),
	}

	fmt.Println("listening on", socketPath)

	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go d.handleConn(conn)
	}
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

func (d *Daemon) handleAttach(conn net.Conn, msg AttachMsg) {
	key := ServerKey{RootDir: msg.RootDir, LanguageID: msg.LanguageID}

	// cancel pending kill if one is waiting
	d.mu.Lock()
	if timer, ok := d.pendingKill[key]; ok {
		timer.Stop()
		delete(d.pendingKill, key)
		fmt.Fprintf(conn, "cancelled pending kill lang=%s\n", msg.LanguageID)
	}
	d.mu.Unlock()

	if existing, ok := d.registry.Get(key); ok {
		d.registry.IncrRef(key)
		fmt.Fprintf(conn, "reused pid=%d lang=%s refs=%d\n", existing.PID, msg.LanguageID, existing.Refs)
		return
	}

	server := &LSPServer{PID: msg.PID, Refs: 1}
	d.registry.Register(key, server)
	fmt.Fprintf(conn, "registered pid=%d lang=%s\n", msg.PID, msg.LanguageID)
}

func (d *Daemon) handleDetach(conn net.Conn, msg DetachMsg) {
	key := ServerKey{RootDir: msg.RootDir, LanguageID: msg.LanguageID}

	server, ok := d.registry.Get(key)
	if !ok {
		fmt.Fprintf(conn, "error: no server for lang=%s\n", msg.LanguageID)
		return
	}

	refs := d.registry.DecrRef(key)
	if refs <= 0 {
		fmt.Fprintf(conn, "grace period started pid=%d lang=%s\n", server.PID, msg.LanguageID)

		timer := time.AfterFunc(10*time.Second, func() {
			d.mu.Lock()
			delete(d.pendingKill, key)
			d.mu.Unlock()

			proc, err := os.FindProcess(server.PID)
			if err == nil {
				proc.Kill()
			}
			d.registry.Remove(key)
			fmt.Printf("grace expired: killed pid=%d lang=%s\n", server.PID, msg.LanguageID)
		})

		d.mu.Lock()
		d.pendingKill[key] = timer
		d.mu.Unlock()
		return
	}

	fmt.Fprintf(conn, "detached lang=%s refs=%d\n", msg.LanguageID, refs)
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer conn.Close()
	conn.Write([]byte("ohm connected\n"))

	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}

		var base Msg
		if err := json.Unmarshal(buf[:n], &base); err != nil {
			fmt.Fprintf(conn, "error: bad message: %s\n", err)
			continue
		}

		switch base.Type {
		case "attach":
			var msg AttachMsg
			json.Unmarshal(buf[:n], &msg)
			d.handleAttach(conn, msg)
		case "detach":
			var msg DetachMsg
			json.Unmarshal(buf[:n], &msg)
			d.handleDetach(conn, msg)
		default:
			fmt.Fprintf(conn, "error: unknown type: %s\n", base.Type)
		}
	}
}
