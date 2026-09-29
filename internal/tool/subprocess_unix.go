//go:build unix

package tool

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// applySubprocessGroup makes the child a process-group leader and kills the
// whole group on cancellation so grandchildren do not outlive a timed-out
// tool. Must run after sandbox wrapping, which discards SysProcAttr, Cancel,
// and WaitDelay.
func applySubprocessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err != nil && !errors.Is(err, syscall.ESRCH) {
			return cmd.Process.Kill() // group kill failed; fall back to the child itself
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
}
