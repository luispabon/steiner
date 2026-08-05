//go:build unix

package mcp

import (
	"os/exec"
	"syscall"
	"time"
)

// applyProcessGroup makes the child a process-group leader and kills the whole
// group on cancellation. The NEGATED pid targets the process group, not the
// single child, so grandchildren spawned by the server do not outlive the
// session. Must be called after sandbox wrapping, which discards SysProcAttr,
// Cancel, and WaitDelay.
func applyProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
}
