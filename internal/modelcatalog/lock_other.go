//go:build !unix

package modelcatalog

// acquireFileLock is a no-op on non-unix platforms because this package has no
// portable cross-process file-locking primitive there. The in-process mutex
// still protects concurrent use of one Store.
func acquireFileLock(string) (func(), error) {
	return func() {}, nil
}
