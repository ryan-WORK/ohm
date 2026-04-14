package main

import (
	"fmt"
	"log/slog"
	"os"
	"slices"

	"github.com/ryan-WORK/ohm/daemon"
)

type daemonArgs struct {
	socketPath string
	debug      bool
}

func parseDaemonArgs(args []string) daemonArgs {
	debug := slices.Contains(args, "--debug")
	args = slices.DeleteFunc(args, func(s string) bool { return s == "--debug" })
	socket := "./tmp/ohm.sock"
	if len(args) > 0 {
		socket = args[0]
	}
	return daemonArgs{socketPath: socket, debug: debug}
}

func main() {
	args := os.Args[1:]

	if len(args) > 0 && args[0] == "--client" {
		if err := daemon.RunClient(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "ohm-client:", err)
			os.Exit(1)
		}
		return
	}

	a := parseDaemonArgs(args)

	level := slog.LevelWarn
	if a.debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	slog.Info("ohm starting")

	if err := daemon.Start(a.socketPath); err != nil {
		slog.Error("daemon error", "err", err)
		os.Exit(1)
	}
}
