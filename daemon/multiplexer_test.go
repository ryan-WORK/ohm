package daemon

import (
	"bufio"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"
)

// --- helpers ---

// testMux wires a Mux to in-process net.Pipe connections so tests can act as
// both the LSP server and any number of Neovim clients.
type testMux struct {
	mux       *Mux
	serverIn  net.Conn // read what the mux forwarded to the LSP server
	serverOut net.Conn // write LSP server responses into the mux
}

func newTestMux(t *testing.T) *testMux {
	t.Helper()
	// stdinPair: mux writes to stdinServer; test reads from stdinClient
	stdinClient, stdinServer := net.Pipe()
	// stdoutPair: mux reads from stdoutClient; test writes to stdoutServer
	stdoutClient, stdoutServer := net.Pipe()

	proc := &Process{
		Stdin:  stdinServer,
		Stdout: stdoutClient,
		PID:    9999,
	}

	mux := newMux(proc)
	go mux.Broadcast()

	t.Cleanup(func() {
		stdoutServer.Close()
		stdinClient.Close()
	})

	return &testMux{
		mux:       mux,
		serverIn:  stdinClient,
		serverOut: stdoutServer,
	}
}

// addClient connects a new client to the mux, returning the client-side conn.
func (tm *testMux) addClient(t *testing.T) (clientConn net.Conn, r *bufio.Reader) {
	t.Helper()
	clientConn, muxConn := net.Pipe()
	tm.mux.AddClient(muxConn)
	return clientConn, bufio.NewReader(clientConn)
}

func sendFrameConn(t *testing.T, conn net.Conn, body []byte) {
	t.Helper()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := WriteFrame(conn, body); err != nil {
		t.Fatalf("sendFrame: %v", err)
	}
}

func recvFrameConn(t *testing.T, r *bufio.Reader, conn net.Conn) []byte {
	t.Helper()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	body, err := ReadFrame(r)
	if err != nil {
		t.Fatalf("recvFrame: %v", err)
	}
	return body
}

func mustMarshal(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func lspRequest(id interface{}, method string) []byte {
	return mustMarshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  map[string]interface{}{},
	})
}

func lspResponse(id interface{}, result interface{}) []byte {
	return mustMarshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func lspNotification(method string) []byte {
	return mustMarshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  map[string]interface{}{},
	})
}

func extractID(t *testing.T, body []byte) json.RawMessage {
	t.Helper()
	var obj struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("extractID unmarshal: %v", err)
	}
	return obj.ID
}

func extractMethod(t *testing.T, body []byte) string {
	t.Helper()
	var obj struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("extractMethod: %v", err)
	}
	return obj.Method
}

// --- peekLSP ---

func TestPeekLSP_request(t *testing.T) {
	body := lspRequest(42, "textDocument/hover")
	p := peekLSP(body)
	if !p.hasID {
		t.Error("expected hasID")
	}
	if !p.hasMethod {
		t.Error("expected hasMethod")
	}
	if p.method != "textDocument/hover" {
		t.Errorf("method: got %q", p.method)
	}
}

func TestPeekLSP_response(t *testing.T) {
	body := lspResponse(7, map[string]interface{}{"result": "ok"})
	p := peekLSP(body)
	if !p.hasID {
		t.Error("expected hasID")
	}
	if p.hasMethod {
		t.Error("expected no method")
	}
}

func TestPeekLSP_notification(t *testing.T) {
	body := lspNotification("textDocument/publishDiagnostics")
	p := peekLSP(body)
	if p.hasID {
		t.Error("expected no id")
	}
	if !p.hasMethod {
		t.Error("expected hasMethod")
	}
	if p.method != "textDocument/publishDiagnostics" {
		t.Errorf("method: got %q", p.method)
	}
}

// --- rewriteID ---

func TestRewriteIDUint(t *testing.T) {
	body := lspRequest(1, "initialize")
	out := rewriteIDUint(body, 999)

	var obj map[string]json.RawMessage
	json.Unmarshal(out, &obj)
	var got uint64
	json.Unmarshal(obj["id"], &got)
	if got != 999 {
		t.Errorf("id: want 999, got %d", got)
	}
}

func TestRewriteIDRaw(t *testing.T) {
	body := lspResponse(999, nil) // global id
	origID := json.RawMessage(`"client-req-1"`)
	out := rewriteIDRaw(body, origID)

	var obj map[string]json.RawMessage
	json.Unmarshal(out, &obj)
	if string(obj["id"]) != `"client-req-1"` {
		t.Errorf("id: got %s", obj["id"])
	}
}

func TestRewriteIDUint_preservesOtherFields(t *testing.T) {
	body := lspRequest(1, "shutdown")
	out := rewriteIDUint(body, 42)
	if extractMethod(t, out) != "shutdown" {
		t.Error("method lost after rewrite")
	}
}

// --- mux routing ---

// TestMux_requestIDRewriting verifies that the mux assigns a global ID when
// forwarding a request and restores the original ID in the response.
func TestMux_requestIDRewriting(t *testing.T) {
	tm := newTestMux(t)
	clientConn, clientR := tm.addClient(t)

	// Send a hover request with client id=5.
	sendFrameConn(t, clientConn, lspRequest(5, "textDocument/hover"))

	// Mux should forward to server with a new global id (not 5).
	serverBody := recvFrameConn(t, bufio.NewReader(tm.serverIn), tm.serverIn)
	globalID := extractID(t, serverBody)
	if string(globalID) == "5" {
		t.Error("mux should rewrite client ID to a global ID")
	}

	// Server responds with global id.
	var gid uint64
	json.Unmarshal(globalID, &gid)
	sendFrameConn(t, tm.serverOut, lspResponse(gid, map[string]string{"result": "hover"}))

	// Client should receive response with its original id=5.
	resp := recvFrameConn(t, clientR, clientConn)
	if string(extractID(t, resp)) != "5" {
		t.Errorf("client id not restored, got: %s", extractID(t, resp))
	}
}

// TestMux_shutdownIntercept verifies that shutdown is answered locally and
// never forwarded to the LSP server, keeping gopls alive.
func TestMux_shutdownIntercept(t *testing.T) {
	tm := newTestMux(t)
	clientConn, clientR := tm.addClient(t)

	sendFrameConn(t, clientConn, lspRequest(1, "shutdown"))

	// Client gets an immediate fake response.
	resp := recvFrameConn(t, clientR, clientConn)
	if string(extractID(t, resp)) != "1" {
		t.Errorf("fake shutdown response id wrong: %s", extractID(t, resp))
	}

	// Nothing should reach the server.
	tm.serverIn.SetDeadline(time.Now().Add(100 * time.Millisecond))
	var buf [1]byte
	if _, err := tm.serverIn.Read(buf[:]); err == nil {
		t.Error("shutdown was forwarded to server, should have been intercepted")
	}
}

// TestMux_exitDropped verifies that exit notifications from clients are
// dropped and never forwarded — gopls must not be killed by a client disconnect.
func TestMux_exitDropped(t *testing.T) {
	tm := newTestMux(t)
	clientConn, _ := tm.addClient(t)

	sendFrameConn(t, clientConn, lspNotification("exit"))

	tm.serverIn.SetDeadline(time.Now().Add(100 * time.Millisecond))
	var buf [1]byte
	if _, err := tm.serverIn.Read(buf[:]); err == nil {
		t.Error("exit notification was forwarded to server, should have been dropped")
	}
}

// TestMux_broadcastNotification verifies that a server-pushed notification
// reaches all connected clients.
func TestMux_broadcastNotification(t *testing.T) {
	tm := newTestMux(t)

	connA, rA := tm.addClient(t)
	connB, rB := tm.addClient(t)

	diag := lspNotification("textDocument/publishDiagnostics")
	sendFrameConn(t, tm.serverOut, diag)

	bodyA := recvFrameConn(t, rA, connA)
	bodyB := recvFrameConn(t, rB, connB)

	if extractMethod(t, bodyA) != "textDocument/publishDiagnostics" {
		t.Errorf("client A: got method %q", extractMethod(t, bodyA))
	}
	if extractMethod(t, bodyB) != "textDocument/publishDiagnostics" {
		t.Errorf("client B: got method %q", extractMethod(t, bodyB))
	}
}

// TestMux_initializeCaching verifies that the second client receives the
// cached initialize response and no second initialize reaches the server.
func TestMux_initializeCaching(t *testing.T) {
	tm := newTestMux(t)

	// --- Client A: full initialize round-trip ---
	connA, rA := tm.addClient(t)
	sendFrameConn(t, connA, lspRequest(1, "initialize"))

	// Mux forwards to server; read and capture global id.
	serverFrame := recvFrameConn(t, bufio.NewReader(tm.serverIn), tm.serverIn)
	var gid uint64
	json.Unmarshal(extractID(t, serverFrame), &gid)

	// Server responds.
	sendFrameConn(t, tm.serverOut, lspResponse(gid, map[string]interface{}{"capabilities": map[string]bool{}}))

	// Client A gets response with its original id=1.
	respA := recvFrameConn(t, rA, connA)
	if string(extractID(t, respA)) != "1" {
		t.Errorf("client A: id wrong: %s", extractID(t, respA))
	}

	// --- Client B: should get cached response, nothing forwarded to server ---
	connB, rB := tm.addClient(t)
	sendFrameConn(t, connB, lspRequest(2, "initialize"))

	respB := recvFrameConn(t, rB, connB)
	if string(extractID(t, respB)) != "2" {
		t.Errorf("client B: id wrong: %s", extractID(t, respB))
	}

	// Nothing new should arrive at the server.
	tm.serverIn.SetDeadline(time.Now().Add(100 * time.Millisecond))
	var buf [1]byte
	if _, err := tm.serverIn.Read(buf[:]); err == nil {
		t.Error("second initialize was forwarded to server; should have used cache")
	}
}

// TestMux_concurrentInitialize verifies that when two clients send initialize
// simultaneously on a fresh mux, only one initialize reaches the server.
// This exercises the initInFlight / initReady synchronization (Bug D fix).
func TestMux_concurrentInitialize(t *testing.T) {
	tm := newTestMux(t)

	connA, rA := tm.addClient(t)
	connB, rB := tm.addClient(t)

	// Both clients fire initialize at roughly the same time.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); sendFrameConn(t, connA, lspRequest(1, "initialize")) }()
	go func() { defer wg.Done(); sendFrameConn(t, connB, lspRequest(2, "initialize")) }()
	wg.Wait()

	// Exactly one initialize should reach the server.
	serverFrame := recvFrameConn(t, bufio.NewReader(tm.serverIn), tm.serverIn)
	if extractMethod(t, serverFrame) != "initialize" {
		t.Fatalf("expected initialize, got %q", extractMethod(t, serverFrame))
	}

	// Verify no second initialize arrives within a short window.
	tm.serverIn.SetDeadline(time.Now().Add(100 * time.Millisecond))
	var buf [1]byte
	if _, err := tm.serverIn.Read(buf[:]); err == nil {
		t.Error("two initializes reached the server; only one should be forwarded")
	}

	// Respond so both clients get their answer.
	var gid uint64
	json.Unmarshal(extractID(t, serverFrame), &gid)
	sendFrameConn(t, tm.serverOut, lspResponse(gid, map[string]interface{}{}))

	// Both clients must receive a response with their original IDs.
	done := make(chan string, 2)
	go func() {
		resp := recvFrameConn(t, rA, connA)
		done <- string(extractID(t, resp))
	}()
	go func() {
		resp := recvFrameConn(t, rB, connB)
		done <- string(extractID(t, resp))
	}()

	ids := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case id := <-done:
			ids[id] = true
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for initialize responses")
		}
	}
	if !ids["1"] || !ids["2"] {
		t.Errorf("expected ids 1 and 2, got: %v", ids)
	}
}
