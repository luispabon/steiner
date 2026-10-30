//go:build !unix || aix

package oauth

import "context"

// acquireTokenLock does nothing on platforms without flock. Refresh is still
// single-flight within this process, but two processes can refresh the same
// token file concurrently and write the token last-wins through the atomic save.
func acquireTokenLock(context.Context, string) (func(), error) {
	return func() {}, nil
}
