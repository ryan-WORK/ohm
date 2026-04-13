package daemon

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/ryan-WORK/ohm/rpc"
)

type Daemon struct {
	registry    *Registry
	pendingKill map[ServerKey]*time.Timer
	mu          sync.Mutex
}

type DetachMsg struct {
	RootDir    string `codec:"root_dir"`
	LanguageID string `codec:"language_id"`
	URI        string `codec:"uri"`
}


type AttachMsg struct {
	RootDir    string   `codec:"root_dir"`
	LanguageID string   `codec:"language_id"`
	Command    string   `codec:"command"`
	Args       []string `codec:"args"`
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
	d.startWatchdog()

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

// handleAttach returns true if proxy goroutines were started and now own conn.
// The caller must return immediately when true — do not read from conn again.
func (d *Daemon) handleAttach(conn net.Conn, msg AttachMsg) bool {
	key := ServerKey{RootDir: msg.RootDir, LanguageID: msg.LanguageID}

	// cancel pending kill if one is waiting
	d.mu.Lock()
	if timer, ok := d.pendingKill[key]; ok {
		timer.Stop()
		delete(d.pendingKill, key)
		fmt.Fprintf(conn, "cancelled pending kill lang=%s\n", msg.LanguageID)
	}
	d.mu.Unlock()

	// reuse: server already running, just increment refs.
	// Connection closes — Neovim's LSP client talks directly to the existing server.
	if existing, ok := d.registry.Get(key); ok {
		d.registry.IncrRef(key)
		fmt.Fprintf(conn, "reused pid=%d lang=%s refs=%d\n", existing.PID, msg.LanguageID, existing.Refs)
		return false
	}

	proc, err := SpawnLSP(msg.Command, msg.Args...)
	if err != nil {
		fmt.Fprintf(conn, "error: spawn: %s\n", err)
		return false
	}
	server := &LSPServer{PID: proc.PID, Refs: 1, Process: proc, LastResponse: time.Now()}
	d.registry.Register(key, server)
	fmt.Fprintf(conn, "registered pid=%d lang=%s\n", proc.PID, msg.LanguageID)

	go io.Copy(proc.Stdin, conn) // neovim → LSP

	// LSP → neovim: timestamp each response
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := proc.Stdout.Read(buf)
			if n > 0 {
				server.LastResponse = time.Now()
				conn.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	return true // proxy owns conn now
}

func (d *Daemon) handleDetach(conn net.Conn, msg DetachMsg) {
	key := ServerKey{RootDir: msg.RootDir, LanguageID: msg.LanguageID}

	server, ok := d.registry.Get(key)
	if !ok {
		fmt.Fprintf(conn, "error: no server for lang=%s\n", msg.LanguageID)
		return
	}

	if msg.URI != "" {
		server.Process.SendNotification("textDocument/didClose", map[string]interface{}{
			"textDocument": map[string]string{"uri": msg.URI},
		})
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

	h := rpc.NewHandler()
	for {
		msg, err := h.Decode(conn)
		if err != nil {
			return
		}
		switch msg.Method {
		case "attach":
			var a AttachMsg
			if err := h.DecodeParam(&a, msg.Params[0]); err != nil {
				fmt.Fprintf(conn, "error: decode attach: %s\n", err)
				continue
			}
			if d.handleAttach(conn, a) {
				return // proxy owns conn — stop reading control messages
			}
		case "detach":
			var a DetachMsg
			if err := h.DecodeParam(&a, msg.Params[0]); err != nil {
				fmt.Fprintf(conn, "error: decode detach: %s\n", err)
				continue
			}
			d.handleDetach(conn, a)
		default:
			fmt.Fprintf(conn, "error: unknown method: %s\n", msg.Method)
		}
	}
}
