//go:build unix

package lsp

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// termGracePeriod is how long the process group has to handle SIGTERM before
// SIGKILL follows.
const termGracePeriod = 100 * time.Millisecond

// setProcessGroup puts the server in its own process group so killProcess can
// signal the whole tree, including helpers the server spawned.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcess terminates the process group with SIGTERM then SIGKILL.
func killProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pgid := -cmd.Process.Pid

	if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("terminate process group: %w", err)
	}

	time.Sleep(termGracePeriod)

	if err := syscall.Kill(pgid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("kill process group: %w", err)
	}
	return nil
}
