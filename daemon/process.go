package daemon

import (
	"fmt"
	"os/exec"
)

type Process struct {
	cmd *exec.Cmd
	PID int
}

func SpawnLSP(command string, args ...string) (*Process, error) {
	cmd := exec.Command(command, args...)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn %s: %w", command, err)
	}

	return &Process{
		cmd: cmd,
		PID: cmd.Process.Pid,
	}, nil
}

func (p *Process) Kill() error {
	return p.cmd.Process.Kill()
}

func (p *Process) Wait() error {
	return p.cmd.Wait()
}
