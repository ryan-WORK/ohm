# ohm

<p align="center">
  <img src="assets/banner.svg" width="320" alt="ohm — meditating hippie daemon"/>
</p>

> one server. no resistance.

A persistent LSP process manager daemon for Neovim. Neovim starts a fresh server per session — ohm replaces that with one shared server per `{root_dir, language}` pair, fixing memory bloat, stuck diagnostics, and monorepo duplication at the daemon layer.

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
- **Watchdog** — kills runaway or frozen servers automatically.
- **Shutdown interception** — intercepts client `shutdown`/`exit` so individual session closes don't kill the shared server.

## Requirements

- Neovim 0.9+
- Go 1.21+ (only if building from source)

## Install

### lazy.nvim — pre-built binary (recommended)

**Step 1** — download the binary for your platform:

```bash
# Linux x86_64
curl -L https://github.com/ryan-WORK/ohm/releases/latest/download/ohm-linux-amd64 -o ~/.local/bin/ohm

# Linux arm64
curl -L https://github.com/ryan-WORK/ohm/releases/latest/download/ohm-linux-arm64 -o ~/.local/bin/ohm

# macOS Apple Silicon
curl -L https://github.com/ryan-WORK/ohm/releases/latest/download/ohm-darwin-arm64 -o ~/.local/bin/ohm

# macOS Intel
curl -L https://github.com/ryan-WORK/ohm/releases/latest/download/ohm-darwin-amd64 -o ~/.local/bin/ohm

chmod +x ~/.local/bin/ohm
```

Make sure `~/.local/bin` is on your `PATH`. Verify with `ohm --help`.

**Step 2** — add the plugin (no `build` hook needed):

```lua
{
  "ryan-WORK/ohm",
  config = function()
    require("ohm").setup()
  end,
}
```

### lazy.nvim — build from source

Requires Go 1.21+.

```lua
{
  "ryan-WORK/ohm",
  build = "./build.sh",
  config = function()
    require("ohm").setup()
  end,
}
```

`build.sh` compiles the binary into `bin/ohm` inside the plugin directory on install and update.

### mason.nvim

> mason registry submission in progress — project needs more stars.

### Manual binary install

```bash
# download
curl -L https://github.com/ryan-WORK/ohm/releases/latest/download/ohm-linux-amd64 -o ohm
chmod +x ohm

# verify checksum (replace hash with value from checksums.txt on the release page)
echo "<sha256>  ohm" | sha256sum -c
```

Pass the path explicitly if not on `PATH`:

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

## Architecture

See [docs/architecture.md](docs/architecture.md) for a deep dive: two-socket design, request flow, ID rewriting, initialize caching, respawn, and the concurrency model.

## License

MIT
