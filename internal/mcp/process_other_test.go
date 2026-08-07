//go:build !unix

package mcp

// processAlive reports whether a process with the given pid still exists.
// Non-unix platforms have no process groups (applyProcessGroup is a no-op), so
// there is no liveness probe to run; report the process as gone so the reaping
// assertion is a no-op there.
func processAlive(pid int) bool {
	return false
}
