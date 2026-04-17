package daemon

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ryan-WORK/ohm/rpc"
)

type Daemon struct {
	registry      *Registry
	pendingKill   map[ServerKey]*time.Timer
	mu            sync.Mutex
	controlSocket string
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

// ServerStatus is returned by the "status" RPC call.
type ServerStatus struct {
	PID          int    `codec:"pid"`
	Lang         string `codec:"lang"`
	RootDir      string `codec:"root_dir"`
	MemoryMB     int    `codec:"memory_mb"`
	Refs         int    `codec:"refs"`
	LastResponse string `codec:"last_response"`
}

func Start(socketPath string) error {
	// If another daemon is already listening, exit cleanly — don't clobber it.
	if conn, err := net.Dial("unix", socketPath); err == nil {
		conn.Close()
		slog.Info("daemon already running, exiting", "socket", socketPath)
		return nil
	}
	os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer ln.Close()

	d := &Daemon{
		registry:      NewRegistry(),
		pendingKill:   make(map[ServerKey]*time.Timer),
		controlSocket: socketPath,
	}

	slog.Info("listening", "socket", socketPath)
	d.startWatchdog()

	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go d.handleConn(conn)
	}
}

// handleAttach spawns or reuses an LSP server and returns the proxy socket path.
func (d *Daemon) handleAttach(msg AttachMsg) (string, error) {
	key := ServerKey{RootDir: msg.RootDir, LanguageID: msg.LanguageID}

	d.mu.Lock()
	if timer, ok := d.pendingKill[key]; ok {
		timer.Stop()
		delete(d.pendingKill, key)
		slog.Info("cancelled pending kill", "lang", msg.LanguageID)
	}
	d.mu.Unlock()

	if existing, ok := d.registry.Get(key); ok {
		d.registry.IncrRef(key)
		slog.Info("reused", "pid", existing.PID, "lang", msg.LanguageID, "refs", existing.Refs)
		return existing.ProxySocket, nil
	}

	proc, err := SpawnLSP(msg.Command, msg.Args...)
	if err != nil {
		return "", fmt.Errorf("spawn: %w", err)
	}

	go captureStderr(proc, msg.LanguageID)

	proxyPath := d.proxySocketPath(key)
	mux := newMux(proc)

	server := &LSPServer{
		PID:         proc.PID,
		Refs:        1,
		Process:     proc,
		Command:     msg.Command,
		Args:        msg.Args,
		ProxySocket: proxyPath,
	}
	server.SetMux(mux)
	d.registry.Register(key, server)

	mux.onExit = func() { d.respawnServer(key) }

	slog.Info("registered", "pid", proc.PID, "lang", msg.LanguageID, "proxy", proxyPath)

	go mux.Broadcast()

	// Block until proxy socket is bound so the path is usable immediately.
	ready := make(chan error, 1)
	go d.listenProxy(server, proxyPath, ready)
	if err := <-ready; err != nil {
		server.Process.Kill()
		d.registry.Remove(key)
		return "", fmt.Errorf("proxy socket: %w", err)
	}

	return proxyPath, nil
}

func (d *Daemon) respawnServer(key ServerKey) {
	// Cancel any pending kill timer — it was set for the crashed process and
	// would otherwise fire on the newly-spawned one.
	d.mu.Lock()
	if timer, ok := d.pendingKill[key]; ok {
		timer.Stop()
		delete(d.pendingKill, key)
		slog.Info("respawn: cancelled pending kill", "lang", key.LanguageID)
	}
	d.mu.Unlock()

	server, ok := d.registry.Get(key)
	if !ok {
		return
	}

	slog.Info("respawning", "lang", key.LanguageID, "prev_pid", server.PID)

	proc, err := SpawnLSP(server.Command, server.Args...)
	if err != nil {
		slog.Error("respawn failed", "lang", key.LanguageID, "err", err)
		if server.proxyLn != nil {
			server.proxyLn.Close()
		}
		os.Remove(server.ProxySocket)
		d.registry.Remove(key)
		return
	}

	go captureStderr(proc, key.LanguageID)

	mux := newMux(proc)
	mux.onExit = func() { d.respawnServer(key) }

	server.mu.Lock()
	server.PID = proc.PID
	server.Process = proc
	server.mux = mux
	server.mu.Unlock()

	go mux.Broadcast()

	slog.Info("respawned", "lang", key.LanguageID, "new_pid", proc.PID)
}

func captureStderr(proc *Process, lang string) {
	if proc.Stderr == nil {
		return
	}
	scanner := bufio.NewScanner(proc.Stderr)
	for scanner.Scan() {
		slog.Warn("lsp stderr", "lang", lang, "pid", proc.PID, "line", scanner.Text())
	}
}

// proxySocketPath returns a stable socket path for the per-server LSP proxy.
// The 4-byte hash prefix is for uniqueness across root+lang pairs, not security.
func (d *Daemon) proxySocketPath(key ServerKey) string {
	h := sha256.Sum256([]byte(key.RootDir + "|" + key.LanguageID))
	name := fmt.Sprintf("ohm-%s-%x.sock", key.LanguageID, h[:4])
	return filepath.Join(filepath.Dir(d.controlSocket), name)
}

func (d *Daemon) listenProxy(server *LSPServer, socketPath string, ready chan<- error) {
	os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		ready <- err
		return
	}
	server.proxyLn = ln
	ready <- nil

	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		server.GetMux().AddClient(conn)
	}
}

func (d *Daemon) handleDetach(msg DetachMsg) {
	key := ServerKey{RootDir: msg.RootDir, LanguageID: msg.LanguageID}

	server, ok := d.registry.Get(key)
	if !ok {
		slog.Warn("detach: no server", "lang", msg.LanguageID)
		return
	}

	if msg.URI != "" {
		server.Process.SendNotification("textDocument/didClose", map[string]interface{}{
			"textDocument": map[string]string{"uri": msg.URI},
		})
	}

	refs := d.registry.DecrRef(key)
	if refs <= 0 {
		d.mu.Lock()
		if _, already := d.pendingKill[key]; already {
			d.mu.Unlock()
			slog.Info("detach: kill already pending", "lang", msg.LanguageID)
			return
		}
		slog.Info("grace period started", "pid", server.PID, "lang", msg.LanguageID)
		timer := time.AfterFunc(10*time.Second, func() {
			d.mu.Lock()
			delete(d.pendingKill, key)
			d.mu.Unlock()

			server.Close()
			d.registry.Remove(key)
			slog.Info("grace expired: killed", "pid", server.PID, "lang", msg.LanguageID)
		})
		d.pendingKill[key] = timer
		d.mu.Unlock()
		return
	}

	slog.Info("detached", "lang", msg.LanguageID, "refs", refs)
}

func (d *Daemon) collectStatus() []ServerStatus {
	d.registry.mu.Lock()
	defer d.registry.mu.Unlock()

	result := make([]ServerStatus, 0, len(d.registry.servers))
	for key, server := range d.registry.servers {
		mb, _ := server.Process.MemoryMB()
		result = append(result, ServerStatus{
			PID:          server.PID,
			Lang:         key.LanguageID,
			RootDir:      key.RootDir,
			MemoryMB:     mb,
			Refs:         server.Refs,
			LastResponse: server.GetMux().LastResponse().Format(time.RFC3339),
		})
	}
	return result
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer conn.Close()
	slog.Info("client connected")

	h := rpc.NewHandler()
	for {
		msg, err := h.Decode(conn)
		if err != nil {
			return
		}
		switch msg.Method {
		case "attach":
			if len(msg.Params) == 0 {
				slog.Error("attach: missing params")
				h.WriteResponse(conn, msg.MsgID, nil)
				continue
			}
			var a AttachMsg
			if err := h.DecodeParam(&a, msg.Params[0]); err != nil {
				slog.Error("decode attach", "err", err)
				h.WriteResponse(conn, msg.MsgID, nil)
				continue
			}
			socketPath, err := d.handleAttach(a)
			if err != nil {
				slog.Error("attach", "err", err)
				h.WriteResponse(conn, msg.MsgID, nil)
				continue
			}
			h.WriteResponse(conn, msg.MsgID, socketPath)

		case "detach":
			if len(msg.Params) == 0 {
				slog.Error("detach: missing params")
				continue
			}
			var a DetachMsg
			if err := h.DecodeParam(&a, msg.Params[0]); err != nil {
				slog.Error("decode detach", "err", err)
				continue
			}
			d.handleDetach(a)

		case "status":
			h.WriteResponse(conn, msg.MsgID, d.collectStatus())

		default:
			slog.Warn("unknown method", "method", msg.Method)
		}
	}
}
