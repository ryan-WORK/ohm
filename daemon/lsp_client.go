package daemon

import (
	"fmt"
	"io"
	"net"
	"os"

	"github.com/ugorji/go/codec"
)

// RunClient implements `ohm --client` mode: connects to the daemon's control
// socket, sends an attach request, receives the per-server proxy socket path,
// then bridges stdin/stdout to that proxy socket.
//
// Args format: --socket <path> --root <dir> --lang <lang> -- <cmd> [args...]
func RunClient(args []string) error {
	socket, root, lang, cmd, err := parseClientArgs(args)
	if err != nil {
		return fmt.Errorf("usage: %w", err)
	}
	if len(cmd) == 0 {
		return fmt.Errorf("missing LSP command after --")
	}

	// Connect to daemon control socket.
	ctrl, err := net.Dial("unix", socket)
	if err != nil {
		return fmt.Errorf("connect to daemon %s: %w", socket, err)
	}

	// Send attach REQUEST (msgpack-rpc type 0).
	var mh codec.MsgpackHandle
	mh.RawToString = true
	enc := codec.NewEncoder(ctrl, &mh)
	err = enc.Encode([]interface{}{
		uint8(0), // type: request
		uint32(1),
		"attach",
		[]interface{}{map[string]interface{}{
			"root_dir":    root,
			"language_id": lang,
			"command":     cmd[0],
			"args":        cmd[1:],
		}},
	})
	if err != nil {
		ctrl.Close()
		return fmt.Errorf("encode attach: %w", err)
	}

	// Read response [type=1, msgid, error, result].
	dec := codec.NewDecoder(ctrl, &mh)
	var resp []interface{}
	if err := dec.Decode(&resp); err != nil {
		ctrl.Close()
		return fmt.Errorf("decode response: %w", err)
	}
	ctrl.Close()

	if len(resp) < 4 {
		return fmt.Errorf("invalid response length %d", len(resp))
	}
	if resp[2] != nil {
		return fmt.Errorf("daemon error: %v", resp[2])
	}
	proxyPath, ok := resp[3].(string)
	if !ok || proxyPath == "" {
		return fmt.Errorf("daemon returned no proxy socket")
	}

	// Connect to the per-server proxy socket (raw LSP JSON-RPC).
	proxy, err := net.Dial("unix", proxyPath)
	if err != nil {
		return fmt.Errorf("connect to proxy %s: %w", proxyPath, err)
	}
	defer proxy.Close()

	// Bidirectional bridge: stdin → proxy, proxy → stdout.
	copyDone := make(chan struct{})
	go func() {
		defer close(copyDone)
		io.Copy(proxy, os.Stdin)
		proxy.(*net.UnixConn).CloseWrite()
	}()
	io.Copy(os.Stdout, proxy)
	<-copyDone
	return nil
}

func parseClientArgs(args []string) (socket, root, lang string, cmd []string, err error) {
	hasSep := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--socket":
			i++
			if i >= len(args) {
				return "", "", "", nil, fmt.Errorf("--socket requires a value")
			}
			socket = args[i]
		case "--root":
			i++
			if i >= len(args) {
				return "", "", "", nil, fmt.Errorf("--root requires a value")
			}
			root = args[i]
		case "--lang":
			i++
			if i >= len(args) {
				return "", "", "", nil, fmt.Errorf("--lang requires a value")
			}
			lang = args[i]
		case "--":
			hasSep = true
			cmd = args[i+1:]
			i = len(args) // consumed; exit loop to run validation below
		}
	}
	if socket == "" {
		err = fmt.Errorf("missing --socket")
	} else if root == "" {
		err = fmt.Errorf("missing --root")
	} else if lang == "" {
		err = fmt.Errorf("missing --lang")
	} else if !hasSep {
		err = fmt.Errorf("missing -- <cmd>")
	}
	return
}
