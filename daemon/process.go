package daemon

import (
	"fmt"
	"io"
	"os/exec"
)

type Process struct {
	cmd    *exec.Cmd
	PID    int
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
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

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn %s: %w", command, err)
	}

	return &Process{
		cmd:    cmd,
		PID:    cmd.Process.Pid,
		Stdin:  stdin,
		Stdout: stdout,
	}, nil
}

func (p *Process) Kill() error {
	return p.cmd.Process.Kill()
}

func (p *Process) Wait() error {
	return p.cmd.Wait()
}
