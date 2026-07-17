//go:build !unix

package usagestats

// otherFileLocker is the fallback locker for platforms without a flock
// equivalent wired up (i.e. not unix). It intentionally does not provide
// cross-process locking; steiner is not currently supported on non-unix
// targets, so this only guards against a build tag needing a fileLocker
// implementation to compile.
type otherFileLocker struct{}

func newFileLocker() fileLocker {
	return &otherFileLocker{}
}

func (*otherFileLocker) lock(uintptr) error {
	return nil
}

func (*otherFileLocker) unlock(uintptr) error {
	return nil
}
