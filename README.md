# ohm

<p align="center">
  <img src="assets/banner.svg" width="320" alt="ohm — meditating hippie daemon"/>
</p>

> one server. no resistance.

A persistent LSP process manager daemon for Neovim. Fixes memory bloat, stuck diagnostics, monorepo server duplication, and session degradation — the recurring pain points in Neovim's LSP lifecycle.

## The Problem

Neovim starts a new LSP server per session, leaks memory, leaves stuck diagnostics on detach, and spawns duplicate servers in monorepos. ohm solves it at the daemon layer.

## How It Works

```
Neovim instances (any number)
      ↕  stdio (LSP JSON-RPC via ohm --client bridge)
  ohm daemon — fan-out multiplexer, request ID rewriting
      ↕  stdio (LSP JSON-RPC)
  LSP servers (gopls, rust-analyzer, tsserver, ...)
```

- **Shared servers** — one LSP process per `{root_dir, language}` pair, shared across all Neovim sessions.
- **Fan-out multiplexer** — rewrites request IDs per client, routes responses back to the correct session.
- **Ref counting** — tracks attached buffers. Server stays alive while any buffer is open.
- **Grace period** — when refs hit 0, waits 10s before killing. Reopen a file within the window to cancel.
- **Diagnostic fence** — sends `textDocument/didClose` before detach to prevent stuck diagnostics.
- **Respawn** — crashed servers are automatically restarted without losing the proxy socket.
- **Watchdog** — kills servers exceeding 1500MB RSS or frozen for 5+ minutes.
- **Shutdown interception** — intercepts client `shutdown`/`exit` so individual session closes don't kill the shared server.

## Requirements

- Neovim 0.9+
- Go 1.21+ (only if building from source)

## Install

### lazy.nvim — pre-built binary (recommended)

```lua
{
  "ryan-WORK/ohm",
  config = function()
    require("ohm").setup()
  end,
}
```

Download the binary for your platform from [Releases](https://github.com/ryan-WORK/ohm/releases) and place it in `~/.local/share/nvim/ohm/bin/ohm` (or anywhere on your PATH).

### lazy.nvim — build from source

```lua
{
  "ryan-WORK/ohm",
  build = "./build.sh",
  config = function()
    require("ohm").setup()
  end,
}
```

Requires Go on your machine. `build.sh` compiles the binary into `bin/ohm` on install and update.

### mason.nvim

> mason registry submission in progress — not available yet.

Once merged:

```
:MasonInstall ohm
```

### Manual

```bash
git clone https://github.com/ryan-WORK/ohm
cd ohm
./build.sh
```

Place `bin/ohm` on your PATH or pass the path explicitly:

```lua
require("ohm").setup({ binary = "/path/to/ohm" })
```

## Configuration

```lua
require("ohm").setup({
  -- Path to ohm binary. Auto-detected from bin/ohm in plugin dir or PATH.
  binary = nil,

  -- Unix socket path for the control channel.
  socket = vim.fn.stdpath("data") .. "/ohm.sock",

  -- Enable verbose daemon logging (useful for debugging LSP issues).
  debug = false,
})
```

## Commands

| Command | Description |
|---------|-------------|
| `:OhmStatus` | Show active servers: PID, language, memory, refs, last response |
| `:OhmRestart` | Stop and restart the daemon |

## Development

```bash
# build
go build -o bin/ohm .

# run tests
go test ./...

# run daemon (silent by default — only warns/errors surface)
mkdir -p tmp && go run . tmp/ohm.sock

# run daemon with verbose logging
mkdir -p tmp && go run . --debug tmp/ohm.sock
```

## License

MIT
