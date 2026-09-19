//go:build !unix

package builtin

import "os/exec"

// setBashProcessGroup is a no-op: outside unix there is no process group.
func setBashProcessGroup(cmd *exec.Cmd) {}

// killBashProcess terminates the shell process directly.
func killBashProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill() // best-effort; Wait reports the outcome
	}
}
