# ohm

> one server. no resistance.

A persistent LSP process manager daemon for Neovim. Fixes memory bloat, stuck diagnostics, monorepo server duplication, and session degradation — the recurring pain points in Neovim's LSP lifecycle.

## The Problem

Neovim's LSP integration starts a new server per session, leaks memory, leaves stuck diagnostics on detach, and spawns duplicate servers in monorepos. This is a systems problem. ohm solves it at the daemon layer.

## How It Works

```
Neovim (Lua shim)
      ↕  msgpack-rpc over unix socket
  ohm daemon (Go)
      ↕  LSP protocol (JSON-RPC over stdio)
  LSP servers (gopls, rust-analyzer, tsserver, ...)
```

- **Registry** — deduplicates LSP servers by `{root_dir, language_id}`. One server per project, shared across buffers.
- **Ref counting** — tracks attached buffers. Server stays alive while any buffer is attached.
- **Grace period** — when refs hit 0, waits 10s before killing. Reattach within the window cancels the kill.
- **Diagnostic fence** — sends `textDocument/didClose` before detach to prevent stuck diagnostics.
- **Watchdog** — kills servers exceeding 1500MB RSS or frozen for 5+ minutes.

## Requirements

- Go 1.21+
- Neovim 0.9+

## Install

### lazy.nvim

```lua
{
  "ryan-WORK/ohm",
  build = "./build.sh",
  config = function()
    require("ohm").setup()
  end,
}
```

`build.sh` compiles the Go binary into `bin/ohm` on install and update. Requires Go on your machine.

### Manual

```bash
git clone https://github.com/ryan-WORK/ohm
cd ohm
./build.sh
```

Add `bin/ohm` to your PATH or set `binary` in the setup call.

## Configuration

```lua
require("ohm").setup({
  -- Path to ohm binary. Auto-detected from bin/ohm in plugin dir or PATH.
  binary = nil,

  -- Unix socket path. Daemon and shim must agree on this.
  socket = vim.fn.stdpath("data") .. "/ohm.sock",
})
```

## Commands

| Command | Description |
|---------|-------------|
| `:OhmStatus` | Show connection status and socket path |
| `:OhmRestart` | Stop and restart the daemon |

## Development

```bash
# run tests
go test ./...

# run daemon directly (uses ./tmp/ohm.sock)
mkdir -p tmp && go run .

# run daemon with custom socket
go run . /tmp/my.sock
```

## V1 Limitations

- **Single buffer per LSP proxy** — the first buffer to attach to a server gets a proxied connection. Subsequent buffers sharing the same server increment the ref count but do not get a proxy (Neovim's built-in client handles their protocol). Full fan-out multiplexing with request ID rewriting is planned for V2.
- **No graceful shutdown** — uses SIGKILL. LSP `shutdown` + `exit` sequence planned for V2.
- **No respawn** — watchdog kills unhealthy servers but does not restart them. Planned for V2.

See [docs/ohm.md](docs/ohm.md) for full architecture and V2 roadmap.

## License

GPL v3. Forks must publish their source under the same license.
