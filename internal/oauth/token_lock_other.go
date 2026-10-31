//go:build !unix || aix

package oauth

import "context"

// acquireTokenLock does nothing on platforms without flock. Refresh is still
// single-flight within this process, but two processes can refresh the same
// token file concurrently and write the token last-wins through the atomic save.
//
// This is a deliberate fail-open, unlike internal/quota/lock_other.go, which
// fails closed: without cross-process exclusion Codex auth keeps working and
// only a concurrent refresh of the same file can invalidate the rotated refresh
// token, while failing closed would break Codex auth on Windows entirely.
func acquireTokenLock(context.Context, string) (func(), error) {
	return func() {}, nil
}
