//go:build !unix

package lsp

import "os/exec"

// killProcess terminates the process using Process.Kill().
func killProcess(cmd *exec.Cmd) error {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
	return nil
}
