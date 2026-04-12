package main

import (
	"fmt"
	"os"
	// Owned
	"github.com/ryan-WORK/ohm/daemon"
)

func main() {
	fmt.Println("ohm starting")

	socketPath := "./tmp/ohm.sock"

	if err := daemon.Start(socketPath); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
