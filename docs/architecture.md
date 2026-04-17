# ohm — Architecture

## Why ohm exists

Neovim's built-in LSP client starts a fresh server process per session. This creates several recurring problems in real-world use:

- **Memory bloat** — gopls for a large Go repo can use 500–1500MB. Three Neovim sessions = three copies.
- **Monorepo duplication** — opening the same root directory in multiple windows spawns redundant servers that index the same files independently.
- **Stuck diagnostics** — when a Neovim session closes, the LSP server receives no `textDocument/didClose`, leaving stale diagnostics on the next open.
- **Session degradation** — long-running LSP servers accumulate state; a server spawned fresh each session never benefits from warmup.

ohm moves the LSP server lifecycle out of Neovim and into a persistent daemon. One server per `{root_dir, language}` pair, shared across every Neovim session, for the lifetime of the workstation session.

---

## Two-socket design

ohm uses two distinct Unix sockets with different protocols:

```
┌─────────────────────────────────────────────────────┐
│  Neovim instance                                     │
│                                                      │
│  lspconfig (ohm --client per buffer)                 │
│       │ stdio  LSP JSON-RPC                          │
│       ▼                                              │
│  ohm --client bridge process                         │
│       │ unix socket  LSP JSON-RPC                   │
│       ▼                                              │
│  [proxy socket]  ◄──── per server, raw LSP           │
└─────────────────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────────────────┐
│  ohm daemon                                          │
│                                                      │
│  Mux (fan-out, ID rewriting)                         │
│       │ stdio  LSP JSON-RPC                          │
│       ▼                                              │
│  LSP server process (gopls, rust-analyzer, ...)      │
└─────────────────────────────────────────────────────┘

  [control socket]  ◄──── msgpack-rpc, persistent channel
         │
         ▼
  Neovim shim (client.lua rpcrequest/rpcnotify)
```

**Control socket** (`ohm.sock`) — speaks msgpack-rpc (Neovim's native RPC protocol). Used by the Lua plugin to send `attach`/`detach`/`status` commands. One persistent connection per Neovim instance.

**Proxy socket** (e.g. `ohm-go-a3f1b2c4.sock`) — speaks raw LSP JSON-RPC. One socket per registered `{root_dir, language}` server. The `ohm --client` bridge connects here and forwards bytes to/from Neovim's lspconfig.

Keeping the protocols separate avoids the corruption that plagued V1, where LSP frames and msgpack-rpc frames shared a single socket.

---

## Request flow

### Attach (new buffer opened)

1. Neovim opens `main.go` in a Go project.
2. lspconfig calls `vim.lsp.rpc.start`, which ohm's `wire_lspconfig` hook has overridden to launch `ohm --client --socket <sock> --root <dir> --lang go -- gopls`.
3. `ohm --client` connects to the control socket and sends a msgpack-rpc `attach` request.
4. The daemon looks up `{root_dir="...", lang="go"}` in the registry:
   - **Hit** — increments ref count, returns existing proxy socket path.
   - **Miss** — spawns a new `gopls` process, creates a `Mux`, binds a proxy socket, registers the server, returns the proxy socket path.
5. `ohm --client` disconnects from the control socket, connects to the proxy socket, and begins bridging `stdin ↔ proxy` bidirectionally.
6. Neovim's lspconfig now believes it is speaking directly to gopls.

### LSP request (e.g. hover)

```
Neovim  →  ohm --client (stdin→proxy)
        →  proxy socket  →  Mux.serveClient
        →  ID rewritten (client id → global id)
        →  WriteFrame to gopls stdin
        →  gopls processes request
        →  gopls stdout  →  Mux.Broadcast
        →  pending map lookup (global id → original client id + conn)
        →  ID restored
        →  WriteFrame to proxy socket
        →  ohm --client (proxy→stdout)  →  Neovim
```

### Server-pushed notification (e.g. publishDiagnostics)

```
gopls stdout  →  Mux.Broadcast
              →  no id, has method  →  broadcast path
              →  WriteFrame to every connected client
              →  all Neovim instances receive diagnostics
```

---

## Request ID rewriting

LSP uses numeric request IDs chosen by the client. With multiple Neovim sessions sharing one gopls, their IDs collide (every session starts at 1).

The Mux maintains a global atomic counter (`nextID`). On each incoming request:

1. The original client ID is saved in a `pending` map keyed by `globalID`.
2. The message body is rewritten with `globalID` before forwarding to gopls.
3. When gopls responds, `Broadcast` looks up `globalID` in the pending map, rewrites the ID back to the original client value, and routes the response to that client's connection only.

Notifications (no ID field) are broadcast to all clients since they are not responses to a specific request.

---

## initialize caching

The LSP `initialize` handshake is expensive: it triggers full project indexing in gopls. ohm ensures it happens exactly once per server lifetime.

```
First client                   Concurrent client           Later client
──────────────                 ─────────────────           ────────────
send initialize
  initInFlight = true ─────►  sees initInFlight=true
  forward to gopls             block on <-initReady
gopls responds
  cache initResponse
  close(initReady)  ──────►  unblocked
  send to client A             rewrite ID, send cached    send cached immediately
```

Three states tracked under `initMu`:
- `initResponse == nil`, `initInFlight == false` → first caller; forward to server
- `initResponse == nil`, `initInFlight == true` → concurrent caller; wait on `initReady` channel
- `initResponse != nil` → cached; return immediately with ID rewrite

The `initialized` notification (sent after `initialize` succeeds) is only forwarded for the first client. Subsequent clients skip it via the same caching path.

---

## Shutdown interception

When a Neovim session closes, lspconfig sends `shutdown` then `exit`. Forwarding these to gopls would kill the shared server.

`serveClient` intercepts both:

- **`shutdown`** — a fake `{"result": null}` response is sent back to the client immediately. The request is never forwarded.
- **`exit`** (notification, no ID) — silently dropped.

gopls never sees either message and stays running.

---

## Ref counting and grace period

Each `LSPServer` tracks a `Refs` count — the number of `ohm --client` bridge processes currently connected to its proxy socket.

- `attach` → `IncrRef`
- `detach` → `DecrRef`

When `Refs` reaches 0, a 10-second timer starts (`pendingKill`). If a new `attach` arrives within the window the timer is cancelled and the server is reused immediately. After 10 seconds the server is shut down gracefully.

This handles the common case of closing and immediately reopening a file, or switching between splits.

---

## Respawn

When a server process exits unexpectedly (gopls crash, OOM kill), `Mux.Broadcast` reads EOF from the process stdout and calls `mux.onExit`, which triggers `respawnServer`.

`respawnServer`:
1. Cancels any in-flight `pendingKill` timer for the key (a crash during the grace period must not let the timer kill the new process).
2. Spawns a fresh LSP process.
3. Creates a new `Mux` for the new process.
4. Swaps `server.Process` and `server.mux` in place under `server.mu`.
5. Starts `Broadcast` on the new mux.

The proxy socket listener (`listenProxy`) keeps running throughout — it holds no reference to the old mux. New connections arriving after the swap go to the new mux automatically.

Existing Neovim clients connected to the old mux will see their `serveClient` goroutines exit (write errors to a dead process), disconnect, and reconnect on the next LSP request via lspconfig.

---

## Watchdog

A goroutine wakes every 30 seconds and checks every registered server:

| Check | Threshold | Action |
|---|---|---|
| RSS memory | > 1500 MB | graceful shutdown + remove |
| Last response age | > 5 minutes | graceful shutdown + remove |

Memory is read from `/proc/{pid}/status` (VmRSS). Last response time is an atomic timestamp updated on every message received from the server's stdout.

Both checks call `server.Close()` which sends a graceful LSP `shutdown`+`exit` sequence before killing the process. `mux.onExit` then triggers a respawn.

---

## Graceful shutdown sequence

`Mux.GracefulShutdown`:

1. Register a synthetic internal `pending` entry (no client, just a `done` channel).
2. Send `{"method":"shutdown","id":<globalID>}` to the server.
3. Wait up to 5 seconds for the response on `done`.
4. Send `{"method":"exit"}` notification.
5. Wait up to 2 seconds for the process to exit.
6. If still running after step 5, `Kill()`.

---

## Concurrency model

| Goroutine | Lifetime | What it does |
|---|---|---|
| `handleConn` | per control connection | decodes msgpack-rpc, dispatches attach/detach/status |
| `Broadcast` | per LSP server | reads server stdout, routes to clients |
| `serveClient` | per proxy client | reads client frames, rewrites IDs, writes to server stdin |
| `sendLoop` | per proxy client | drains send channel, writes frames to client conn |
| `listenProxy` | per LSP server | accepts new proxy connections |
| `captureStderr` | per LSP server | pipes server stderr to slog |
| `watchdog` | singleton | periodic memory + frozen checks |
| `respawnServer` | on crash | runs as `go m.onExit()` from Broadcast |

Shared state and its lock:

| State | Lock |
|---|---|
| `registry.servers` | `registry.mu` |
| `daemon.pendingKill` | `daemon.mu` |
| `mux.clients` | `mux.clientsMu` (RWMutex) |
| `mux.pending` | `mux.pendingMu` |
| `mux.initResponse / initInFlight` | `mux.initMu` |
| `server.mux / server.Process` | `server.mu` (RWMutex) |
| `mux.lastNs` | atomic |
| `mux.nextID` | atomic |
