//go:build unix

package lsp

import (
	"os/exec"
	"syscall"
	"time"
)

// killProcess terminates the process group with SIGTERM then SIGKILL if needed.
func killProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}

	// Try SIGTERM first
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
		// Process may already be dead
		return nil
	}

	// Wait a short time for graceful shutdown
	time.Sleep(100 * time.Millisecond)

	// Force kill with SIGKILL
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		// Process may already be dead
		return nil
	}

	return nil
}
