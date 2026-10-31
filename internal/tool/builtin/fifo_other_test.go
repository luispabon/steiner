//go:build !unix

package builtin

import "testing"

// mustMkfifo skips: this platform has no named pipes.
func mustMkfifo(t *testing.T, path string) {
	t.Helper()
	t.Skip("named pipes unsupported on this platform")
}
