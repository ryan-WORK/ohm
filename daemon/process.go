package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type Process struct {
	cmd    *exec.Cmd
	PID    int
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Stderr io.ReadCloser
}

func SpawnLSP(command string, args ...string) (*Process, error) {
	cmd := exec.Command(command, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn %s: %w", command, err)
	}

	return &Process{
		cmd:    cmd,
		PID:    cmd.Process.Pid,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	}, nil
}

func (p *Process) Kill() error {
	return p.cmd.Process.Kill()
}

func (p *Process) Wait() error {
	return p.cmd.Wait()
}

// SendNotification writes an LSP JSON-RPC notification to the process stdin.
func (p *Process) SendNotification(method string, params interface{}) error {
	body, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	msg := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	_, err = fmt.Fprint(p.Stdin, msg)
	return err
}

// MemoryMB reads VmRSS from /proc/{pid}/status. Returns MB.
func (p *Process) MemoryMB() (int, error) {
	path := fmt.Sprintf("/proc/%d/status", p.PID)
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0, fmt.Errorf("unexpected VmRSS line: %s", line)
			}
			kb, err := strconv.Atoi(fields[1])
			if err != nil {
				return 0, fmt.Errorf("parse VmRSS: %w", err)
			}
			return kb / 1024, nil
		}
	}
	return 0, fmt.Errorf("VmRSS not found in %s", path)
}
