# ohm

> one server. no resistance.

An LSP process manager daemon written in Go.

## What it does

Manages LSP server lifecycles outside of Neovim. Fixes memory bloat, stuck diagnostics, monorepo server duplication, and session degradation by owning LSP processes in a persistent daemon.

## Architecture

```
Neovim (Lua shim)
      ↕  msgpack-rpc over unix socket
  Go Daemon (ohm)
      ↕  LSP protocol (JSON-RPC over stdio/pipe)
  LSP Servers (gopls, rust-analyzer, tsserver, etc.)
```

## Project structure

```
ohm/
├── main.go              # entry point, starts daemon
├── daemon/
│   ├── daemon.go        # unix socket server, connection handling
│   ├── registry.go      # LSP server registry (deduplication)
│   └── process.go       # child process spawn/kill/wait
└── rpc/
    └── msgpack.go       # msgpack-rpc codec (not yet implemented)
```

## Running

```bash
go run .
```

Listens on `./tmp/ohm.sock`. Connect with:

```bash
nc -U ./tmp/ohm.sock
```

## Status

See [docs/ohm.md](docs/ohm.md) for full progress and roadmap.
