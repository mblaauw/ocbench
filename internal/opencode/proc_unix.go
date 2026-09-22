//go:build unix

package opencode

import (
	"errors"
	"os/exec"
	"syscall"
)

// configureProcessGroup places cmd in its own process group. opencode may spawn
// grandchildren (language servers, MCP servers); killing the group reaches them
// all, where killing the direct child would leave them running.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateProcessGroup sends SIGTERM to cmd's process group.
func terminateProcessGroup(cmd *exec.Cmd) error {
	return signalProcessGroup(cmd, syscall.SIGTERM)
}

// killProcessGroup sends SIGKILL to cmd's process group.
func killProcessGroup(cmd *exec.Cmd) error {
	return signalProcessGroup(cmd, syscall.SIGKILL)
}

// signalProcessGroup sends sig to cmd's process group. It is a no-op before the
// process has started and tolerates an already-exited group (ESRCH).
func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
