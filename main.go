package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/ryan-WORK/ohm/daemon"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--client" {
		if err := daemon.RunClient(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "ohm-client:", err)
			os.Exit(1)
		}
		return
	}

	slog.Info("ohm starting")

	socketPath := "./tmp/ohm.sock"
	if len(os.Args) > 1 {
		socketPath = os.Args[1]
	}

	if err := daemon.Start(socketPath); err != nil {
		slog.Error("daemon error", "err", err)
		os.Exit(1)
	}
}
