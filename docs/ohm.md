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

## Done

### Skeleton + entry point
- `main.go` starts daemon, passes socket path
- `go run .` boots and blocks on socket

### Unix socket server (`daemon/daemon.go`)
- Listens on unix socket path
- Removes stale socket on start
- Accepts connections in a loop
- Spawns goroutine per connection (`go d.handleConn(conn)`)
- `Daemon` struct holds shared registry — state shared across goroutines without globals

### Connection handling
- Sends `ohm connected` on connect
- Reads in loop with `conn.Read` into `[]byte` buffer
- Prints received bytes to stdout
- Returns cleanly on disconnect

### Server registry (`daemon/registry.go`)
- `ServerKey{RootDir, LanguageID}` — deduplication key
- `Registry` with `sync.Mutex` — safe concurrent access
- Full CRUD: `Get`, `Register`, `Remove`

### Process management (`daemon/process.go`)
- `SpawnLSP(command, args...)` — starts child process, returns PID
- `Kill()` — sends kill signal
- `Wait()` — reaps process, prevents zombies
- Uses `cmd.Start()` not `cmd.Run()` — non-blocking spawn

### Go concepts covered
- Package structure and `package main` vs library packages
- Error return convention (`error` as value, `fmt.Errorf("%w", err)` wrapping)
- `defer` — cleanup at function exit
- Goroutines — `go func()`, ~2KB cost vs thread's ~1MB
- Structs and receiver methods
- `sync.Mutex` — protecting shared state
- `make(map[K]V)` — map initialization
- Variadic args (`args ...string`)
- `cmd.Start()` vs `cmd.Run()`
- `buf[:n]` — always slice to actual bytes read

---

### LSP binary resolution
- Daemon does NOT resolve LSP paths — Neovim does
- Lua shim intercepts `LspAttach`, sends PID + metadata to daemon
- Daemon takes over lifecycle from there — no config needed from user

---

## Done (continued)

### Message protocol (`daemon/daemon.go`)
- `Msg{Type}` — peek type before full decode
- `AttachMsg{RootDir, LanguageID, PID}` — register new server
- `DetachMsg{RootDir, LanguageID}` — decrement refs, schedule kill
- JSON over unix socket for now — swaps to msgpack-rpc when Lua shim is built

### Ref counting
- `IncrRef` / `DecrRef` on registry
- Attach to existing server → reuse, increment refs
- Detach → decrement refs
- refs = 0 → start grace period timer

### Grace period kill (`daemon/daemon.go`)
- `pendingKill map[ServerKey]*time.Timer` on Daemon struct
- `time.AfterFunc(10s, ...)` — fires kill in goroutine after delay
- Reattach within grace period → `timer.Stop()` cancels kill
- refs = 0 + grace expired → `os.FindProcess` + `Kill()` + registry remove

### msgpack-rpc decoder (`rpc/rpc.go`)
- `Message{Type, MsgID, Method, Params}` — unified type for requests + notifications
- `Handler.Decode(r io.Reader)` — reads one frame, blocks until data
- Handles type 0 (request) and type 2 (notification)
- Dep: `github.com/ugorji/go/codec`

---

## TODO

### Wire msgpack-rpc into daemon
- [ ] Replace `handleConn` JSON parsing with `rpc.Handler.Decode`
- [ ] Dispatch on `msg.Method` ("attach", "detach") instead of `msg.Type` JSON field
- [ ] Run `go get github.com/ugorji/go/codec`

### LSP stdio pipe
- [ ] Wire `cmd.Stdin` / `cmd.Stdout` to pass JSON-RPC through
- [ ] Proxy between Neovim connection and LSP server process

### Remaining order
1. `go get github.com/ugorji/go/codec`
2. Wire msgpack-rpc decoder into `handleConn`
3. LSP stdio pipe proxy
4. Process supervisor / watchdog
5. Lua shim

### Process supervisor
- [ ] Track memory via `/proc/{pid}/status`
- [ ] Last-response timestamp (detect frozen servers)
- [ ] Watchdog goroutine — periodic health check loop

### Diagnostic fence
- [ ] On detach, send `textDocument/didClose` before clearing diagnostics
- [ ] Brief mutex hold to eliminate stuck-diagnostic race

### Lua shim
- [ ] `jobstart()` to launch ohm binary
- [ ] Unix socket channel to daemon
- [ ] Forward `LspAttach`, `LspDetach`, `BufDelete` via `vim.rpcnotify()`
- [ ] User commands: `:OhmStatus`, `:OhmRestart`

### MVP definition
1. Binary speaks msgpack-rpc over unix socket
2. Registry deduplicates by `{root_dir, language_id}`
3. Ref-counted lifecycle
4. Lua shim wires attach/detach events

---

## Language: Go

Go chosen over Zig (original plan) as learning vehicle. Goroutines map naturally to the per-connection concurrency model. Ecosystem has ready msgpack libraries. Single binary output, no GC pause issues at daemon scale.
