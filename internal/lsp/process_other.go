//go:build !unix

package lsp

import (
	"fmt"
	"os/exec"
)

// setProcessGroup is a no-op: outside unix there is no process group to place
// the server in, and killProcess terminates the process directly.
func setProcessGroup(cmd *exec.Cmd) {}

// killProcess terminates the process.
func killProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil {
		return fmt.Errorf("kill process: %w", err)
	}
	return nil
}
