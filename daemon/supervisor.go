package daemon

import (
	"fmt"
	"time"
)

const (
	memLimitMB      = 1500
	frozenThreshold = 5 * time.Minute
	watchInterval   = 30 * time.Second
)

// startWatchdog launches a goroutine that periodically checks all registered
// LSP servers for memory bloat or frozen state, killing offenders.
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
			fmt.Printf("watchdog: memory limit exceeded pid=%d lang=%s mem=%dMB — killing\n",
				server.PID, key.LanguageID, mb)
			server.Process.Kill()
			d.registry.Remove(key)
			continue
		}

		if time.Since(server.LastResponse) > frozenThreshold {
			fmt.Printf("watchdog: frozen server pid=%d lang=%s last_response=%s — killing\n",
				server.PID, key.LanguageID, server.LastResponse.Format(time.RFC3339))
			server.Process.Kill()
			d.registry.Remove(key)
		}
	}
}
