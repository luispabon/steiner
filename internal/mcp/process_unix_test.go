//go:build unix

package mcp

import (
	"errors"
	"syscall"
)

// processAlive reports whether a process with the given pid still exists.
func processAlive(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
