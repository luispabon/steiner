//go:build !unix

package metadata

// acquireFileLock is a no-op on non-unix platforms because this package has no
// portable cross-process file-locking primitive there.
func acquireFileLock(string) (func(), error) {
	return func() {}, nil
}
