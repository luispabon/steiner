//go:build unix && !aix

package oauth

// tokenLockSupported mirrors the build tags of token_lock_unix.go: tests that
// need cross-process exclusion skip where acquireTokenLock is a no-op.
const tokenLockSupported = true
