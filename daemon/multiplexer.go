package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const clientSendBufSize = 64

// Client is one Neovim connection multiplexed onto an LSP server.
type Client struct {
	conn      net.Conn
	sendCh    chan []byte
	closeOnce sync.Once
}

func newClient(conn net.Conn) *Client {
	c := &Client{
		conn:   conn,
		sendCh: make(chan []byte, clientSendBufSize),
	}
	go c.sendLoop()
	return c
}

// sendLoop drains the send channel and writes to conn. Closes conn when done.
func (c *Client) sendLoop() {
	defer c.conn.Close()
	for body := range c.sendCh {
		if err := WriteFrame(c.conn, body); err != nil {
			for range c.sendCh {
			}
			return
		}
	}
}

// write enqueues a message for sending. Non-blocking: drops if buffer full.
func (c *Client) write(body []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("client closed")
		}
	}()
	select {
	case c.sendCh <- body:
		return nil
	default:
		return fmt.Errorf("send buffer full, dropping")
	}
}

// shutdown closes the send channel, draining sendLoop.
func (c *Client) shutdown() {
	c.closeOnce.Do(func() {
		close(c.sendCh)
	})
}

// pendingReq tracks an in-flight LSP request.
type pendingReq struct {
	client     *Client
	originalID json.RawMessage
	method     string         // LSP method, for caching initialize response
	done       chan<- struct{} // non-nil for internal requests (graceful shutdown)
}

// Mux multiplexes multiple Neovim connections onto one LSP process.
type Mux struct {
	proc *Process

	clientsMu sync.RWMutex
	clients   []*Client

	pendingMu sync.Mutex
	pending   map[uint64]*pendingReq
	nextID    atomic.Uint64

	lastNs atomic.Int64 // UnixNano of last LSP response, read by supervisor

	// initResponse caches the initialize response body (with global ID).
	// Once set, new clients get this instead of a real initialize round-trip.
	initMu       sync.RWMutex
	initResponse []byte

	onExit func() // called when LSP stdout closes
}

func newMux(proc *Process) *Mux {
	m := &Mux{
		proc:    proc,
		pending: make(map[uint64]*pendingReq),
	}
	m.lastNs.Store(time.Now().UnixNano())
	return m
}

// LastResponse returns when the LSP server last sent a message.
func (m *Mux) LastResponse() time.Time {
	return time.Unix(0, m.lastNs.Load())
}

// AddClient registers a new Neovim connection and starts serving it.
func (m *Mux) AddClient(conn net.Conn) {
	c := newClient(conn)
	m.clientsMu.Lock()
	m.clients = append(m.clients, c)
	m.clientsMu.Unlock()
	go m.serveClient(c)
}

func (m *Mux) removeClient(c *Client) {
	m.clientsMu.Lock()
	defer m.clientsMu.Unlock()
	for i, cl := range m.clients {
		if cl == c {
			m.clients = append(m.clients[:i], m.clients[i+1:]...)
			c.shutdown()
			return
		}
	}
}

// serveClient reads LSP frames from one Neovim client, rewrites request IDs,
// and forwards to LSP stdin. Intercepts initialize/initialized for reuse.
func (m *Mux) serveClient(c *Client) {
	defer m.removeClient(c)

	r := bufio.NewReader(c.conn)
	for {
		body, err := ReadFrame(r)
		if err != nil {
			return
		}

		p := peekLSP(body)

		// Request (has id + method).
		if p.hasID && p.hasMethod {
			// Intercept shutdown: send fake success, don't forward.
			if p.method == "shutdown" {
				fake, _ := json.Marshal(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      0,
					"result":  nil,
				})
				c.write(rewriteIDRaw(fake, p.rawID))
				continue
			}

			// initialize: if already done, return cached response immediately.
			m.initMu.RLock()
			cached := m.initResponse
			m.initMu.RUnlock()

			if p.method == "initialize" && cached != nil {
				out := rewriteIDRaw(cached, p.rawID)
				c.write(out)
				continue
			}

			globalID := m.nextID.Add(1)
			m.pendingMu.Lock()
			m.pending[globalID] = &pendingReq{
				client:     c,
				originalID: p.rawID,
				method:     p.method,
			}
			m.pendingMu.Unlock()
			body = rewriteIDUint(body, globalID)
		}

		// Notification (no id): drop exit from clients.
		if !p.hasID && p.hasMethod && p.method == "exit" {
			continue
		}

		if err := WriteFrame(m.proc.Stdin, body); err != nil {
			slog.Error("lsp stdin write", "err", err)
			return
		}
	}
}

// Broadcast reads LSP stdout and routes to clients. Runs until stdout closes.
func (m *Mux) Broadcast() {
	r := bufio.NewReader(m.proc.Stdout)
	for {
		body, err := ReadFrame(r)
		if err != nil {
			if m.onExit != nil {
				go m.onExit()
			}
			return
		}
		m.lastNs.Store(time.Now().UnixNano())

		p := peekLSP(body)

		// Response (has id, no method): route to originating client only.
		if p.hasID && !p.hasMethod {
			var globalID uint64
			if err := json.Unmarshal(p.rawID, &globalID); err == nil {
				m.pendingMu.Lock()
				req, ok := m.pending[globalID]
				if ok {
					delete(m.pending, globalID)
				}
				m.pendingMu.Unlock()

				if ok {
					if req.client != nil {
						out := rewriteIDRaw(body, req.originalID)
						// Cache the initialize response for future clients.
						if req.method == "initialize" {
							m.initMu.Lock()
							if m.initResponse == nil {
								m.initResponse = body // keep global ID; rewrite on send
							}
							m.initMu.Unlock()
						}
						if err := req.client.write(out); err != nil {
							slog.Warn("client write", "err", err)
						}
					} else if req.done != nil {
						req.done <- struct{}{}
					}
				}
			}
			continue
		}

		// Notification or server-initiated request: broadcast to all clients.
		m.clientsMu.RLock()
		snap := make([]*Client, len(m.clients))
		copy(snap, m.clients)
		m.clientsMu.RUnlock()

		for _, c := range snap {
			if err := c.write(body); err != nil {
				slog.Warn("broadcast write", "err", err)
			}
		}
	}
}

// GracefulShutdown sends LSP shutdown+exit, waits for clean exit, then kills.
func (m *Mux) GracefulShutdown() {
	globalID := m.nextID.Add(1)
	done := make(chan struct{}, 1)
	doneSend := (chan<- struct{})(done)

	m.pendingMu.Lock()
	m.pending[globalID] = &pendingReq{done: doneSend}
	m.pendingMu.Unlock()

	body, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      globalID,
		"method":  "shutdown",
		"params":  nil,
	})
	if err := WriteFrame(m.proc.Stdin, body); err != nil {
		slog.Error("graceful shutdown: send request", "err", err)
		m.proc.Kill()
		return
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		slog.Warn("graceful shutdown: timeout waiting for response, sending exit anyway")
	}

	m.proc.SendNotification("exit", nil)

	exitDone := make(chan struct{})
	go func() {
		m.proc.Wait()
		close(exitDone)
	}()
	select {
	case <-exitDone:
	case <-time.After(2 * time.Second):
		slog.Warn("graceful shutdown: process did not exit, killing")
		m.proc.Kill()
	}
}

// lspPeek holds routing-relevant fields parsed from an LSP message.
type lspPeek struct {
	hasID     bool
	hasMethod bool
	rawID     json.RawMessage
	method    string
}

func peekLSP(body []byte) lspPeek {
	var obj struct {
		ID     *json.RawMessage `json:"id"`
		Method *string          `json:"method"`
	}
	_ = json.Unmarshal(body, &obj)
	p := lspPeek{
		hasID:     obj.ID != nil,
		hasMethod: obj.Method != nil,
	}
	if obj.ID != nil {
		p.rawID = json.RawMessage(*obj.ID)
	}
	if obj.Method != nil {
		p.method = *obj.Method
	}
	return p
}

// rewriteIDUint replaces the "id" field in a JSON body with a uint64.
func rewriteIDUint(body []byte, id uint64) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	v, _ := json.Marshal(id)
	obj["id"] = v
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}

// rewriteIDRaw replaces the "id" field with arbitrary JSON (restores original client ID).
func rewriteIDRaw(body []byte, id json.RawMessage) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	obj["id"] = id
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}
