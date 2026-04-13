# ohm

> one server. no resistance.

An LSP process manager daemon written in Go.

## The Problem

Neovim's LSP integration has well-documented, recurring pain points:

1. **Memory bloat and leaks** — attaching an LSP to a large file can push memory to 1.3GB. Even after stopping or detaching the client, memory doesn't get released.
2. **Stuck diagnostics** — if an LSP client detaches while the server is still emitting diagnostics, you end up with stuck diagnostics that won't clear. A race condition in the Lua layer.
3. **Session degradation** — after several hours, responsiveness degrades. Root cause: too many active buffers and LSP clients stacking up.
4. **Monorepo server spawning** — in large TypeScript monorepos, Neovim starts a new TS server every time you switch to a different package, causing ~1 minute delays. VS Code handles this with smarter server sharing.
5. **Formatter conflicts / save freezes** — saving a large file with auto-formatting and an LSP running can freeze the editor entirely for 10+ seconds.

**The gap:** a low-level daemon that manages LSP server lifecycles properly. This is a systems problem, not a Lua problem.

---

## Architecture

```
Neovim (Lua shim)
      ↕  msgpack-rpc over unix socket
  Go Daemon (ohm)
      ↕  LSP protocol (JSON-RPC over stdio/pipe)
  LSP Servers (gopls, rust-analyzer, tsserver, etc.)
```

---

## V1 — Shipped

### Entry point (`main.go`)
- Accepts optional socket path as CLI arg — defaults to `./tmp/ohm.sock`
- Passes path to `daemon.Start`

### Unix socket server (`daemon/daemon.go`)
- Listens on unix socket, removes stale socket on start
- Goroutine per connection via `go d.handleConn(conn)`
- `Daemon` struct holds shared registry + pending kill map

### Server registry (`daemon/registry.go`)
- `ServerKey{RootDir, LanguageID}` — deduplication key
- `Registry` with `sync.Mutex` — safe concurrent access
- `Get`, `Register`, `Remove`, `IncrRef`, `DecrRef`

### Process management (`daemon/process.go`)
- `SpawnLSP(command, args...)` — non-blocking spawn via `cmd.Start()`
- `stdin`/`stdout` pipes exposed on `Process` struct
- `Kill()`, `Wait()`
- `SendNotification(method, params)` — writes LSP-framed JSON-RPC to stdin
- `MemoryMB()` — reads `VmRSS` from `/proc/{pid}/status`

### LSP binary resolution
- Daemon does not resolve LSP paths — Neovim does
- Lua shim reads `client.config.cmd` from `LspAttach` and sends it in the attach message

### msgpack-rpc decoder (`rpc/rpc.go`)
- `Message{Type, MsgID, Method, Params}` — unified type for requests + notifications
- `Handler.Decode(r io.Reader)` — reads one frame, blocks until data
- `Handler.DecodeParam(dst, src)` — round-trip re-decode into typed struct
- `toUint64` handles ugorji's variable integer types
- `RawToString = true` — raw bytes decode as strings

### Control message protocol (`daemon/daemon.go`)
- `AttachMsg{RootDir, LanguageID, Command, Args}` — spawn or reuse server
- `DetachMsg{RootDir, LanguageID, URI}` — decrement refs, send didClose
- msgpack-rpc over unix socket, dispatched on `msg.Method`

### Connection ownership model
- First message on a connection must be attach or detach
- After a successful attach (new server): proxy goroutines take ownership of conn, `handleConn` returns — no read race
- Reuse path (existing server): increments refs, closes conn — V2 adds fan-out multiplexer

### Ref counting + grace period kill
- `IncrRef` / `DecrRef` on registry
- refs = 0 → `time.AfterFunc(10s, kill)` — grace period
- Reattach within grace → `timer.Stop()` cancels kill

### LSP stdio proxy
- `io.Copy(proc.Stdin, conn)` — Neovim → LSP
- Stdout reader loop — timestamps `server.LastResponse` on each response, writes to conn

### Diagnostic fence
- On detach: sends `textDocument/didClose` to LSP stdin before decrementing refs
- Eliminates stuck-diagnostic race condition

### Process supervisor (`daemon/supervisor.go`)
- Watchdog goroutine — checks all servers every 30s
- Kills servers exceeding 1500MB RSS
- Kills servers with no response for 5+ minutes

### Lua shim (`lua/ohm/`)
- `client.lua` — `sockconnect` + `rpcnotify` wrappers
- `init.lua` — `setup()`, `LspAttach`/`LspDetach`/`BufDelete` autocmds, `:OhmStatus`, `:OhmRestart`
- `plugin/ohm.lua` — lazy.nvim entry point
- `build.sh` — compiles binary into `bin/ohm` for lazy.nvim `build` hook

### CI/CD (`.github/workflows/ci.yml`)
- Test + build on push/PR
- Release job: cross-compiles 4 binaries, uploads to GitHub Releases on tag push

### Tests
- `daemon/registry_test.go` — CRUD, ref counting, missing key
- `daemon/process_test.go` — LSP notification framing
- `rpc/rpc_test.go` — notification decode, request decode, `DecodeParam` round-trip

---

## Language: Go

Go chosen over Zig (original plan) as learning vehicle. Goroutines map naturally to the per-connection concurrency model. Ecosystem has ready msgpack libraries. Single binary output, no GC pause issues at daemon scale.

---

## V2 Roadmap

### Must-fix for correctness

#### Fan-out multiplexer + request ID rewriting
Current MVP limitation: only one Neovim connection can proxy to a given LSP server. A second buffer attaching to the same server (reuse path) increments refs but gets no proxy connection — Neovim falls back to its own LSP client.

Full fix requires:
- `LSPServer.clients []net.Conn` — all connected Neovim conns for this server
- Single `proc.Stdout` reader that broadcasts to all clients
- **Request ID rewriting** — each client gets a remapped ID namespace; daemon translates IDs on the way in (Neovim → LSP) and out (LSP → Neovim). Without this, two clients sending request ID 42 both receive the response for 42, corrupting both sessions.

This is the largest V2 item.

#### Graceful LSP shutdown
Currently uses `os.Kill` (SIGKILL). Should send LSP `shutdown` request + `exit` notification first, giving the server a chance to flush state. Fall back to SIGKILL after a timeout.

#### Shim reconnect
If the daemon restarts, the Lua shim should detect the broken connection and reconnect automatically rather than requiring a Neovim restart.

### Reliability

#### Respawn on crash
Watchdog kills frozen/bloated servers but does not respawn them. Neovim is left with no LSP. V2 should respawn and re-proxy.

#### LSP stderr capture
`proc.Stderr` is currently discarded. Capture it and surface via `:OhmStatus` or a log file — essential for debugging gopls panics.

#### Broadcast backpressure
`server.broadcast()` writing to slow Neovim clients will block all other clients sharing that server. Use non-blocking writes with per-client send buffers or drop + disconnect slow clients.

### Observability

#### Rich `:OhmStatus`
Show all running servers: PID, language, memory (MB), ref count, last response time, uptime.

#### Structured logging
Replace `fmt.Printf` with `slog` — log levels, structured fields, optional JSON output for tooling.

### Protocol

#### Proper msgpack-rpc responses
Currently writes plain text (`"registered pid=123\n"`) back to Neovim. Should send type 1 (response) msgpack-rpc frames so the Lua shim can handle errors programmatically.

### Packaging

#### Pre-built binaries
Publish `ohm-linux-amd64`, `ohm-linux-arm64`, `ohm-darwin-amd64`, `ohm-darwin-arm64` to GitHub Releases on tag push. Lua `build` function downloads the correct binary instead of requiring Go.

#### mason.nvim registry
Submit to mason.nvim registry so users can install via `:MasonInstall ohm`.
