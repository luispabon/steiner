//go:build !unix

package mcp

import "os/exec"

// applyProcessGroup is a no-op on platforms without process groups.
func applyProcessGroup(cmd *exec.Cmd) {
	// Process groups are not available on this platform.
}
