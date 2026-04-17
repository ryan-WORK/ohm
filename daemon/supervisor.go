package daemon

import (
	"log/slog"
	"time"
)

const (
	memLimitMB      = 1500          // kill server if RSS exceeds this
	frozenThreshold = 5 * time.Minute // kill server if no response received in this window
	watchInterval   = 30 * time.Second
)

// startWatchdog launches a background goroutine that periodically checks each
// registered LSP server for runaway memory usage or a frozen response stream.
// Servers that fail either check are killed; mux.onExit triggers a respawn.
func (d *Daemon) startWatchdog() {
	go func() {
		for {
			time.Sleep(watchInterval)
			d.checkServers()
		}
	}()
}

func (d *Daemon) checkServers() {
	d.registry.mu.Lock()
	keys := make([]ServerKey, 0, len(d.registry.servers))
	for k := range d.registry.servers {
		keys = append(keys, k)
	}
	d.registry.mu.Unlock()

	for _, key := range keys {
		server, ok := d.registry.Get(key)
		if !ok {
			continue
		}

		mb, err := server.Process.MemoryMB()
		if err == nil && mb > memLimitMB {
			slog.Warn("watchdog: memory limit exceeded, killing",
				"pid", server.PID, "lang", key.LanguageID, "mem_mb", mb)
			server.Close()
			d.registry.Remove(key)
			continue
		}

		if time.Since(server.GetMux().LastResponse()) > frozenThreshold {
			slog.Warn("watchdog: frozen server, killing",
				"pid", server.PID, "lang", key.LanguageID,
				"last_response", server.GetMux().LastResponse().Format(time.RFC3339))
			server.Close()
			d.registry.Remove(key)
		}
	}
}
