//go:build unix

package builtin

import (
	"errors"
	"os/exec"
	"syscall"
)

// setBashProcessGroup makes the shell a process-group leader so killBashProcess
// can take down background children the shell spawned. Must run after sandbox
// wrapping, which may replace the command.
func setBashProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killBashProcess SIGKILLs the shell's whole process group.
func killBashProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		_ = cmd.Process.Kill() // group kill failed; fall back to the shell itself
	}
}
