//go:build unix

package builtin

import (
	"syscall"
	"testing"
)

// mustMkfifo creates a named pipe at path, skipping when the platform or
// filesystem refuses one.
func mustMkfifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}
}
