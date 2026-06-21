//go:build !unix

package usagestats

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
