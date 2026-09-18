//go:build !unix

package usagestats

import (
	"errors"
	"testing"
)

func TestOtherFileLockerRejectsUnlockedPersistence(t *testing.T) {
	if err := newFileLocker().lock(0); !errors.Is(err, errLockingUnsupported) {
		t.Fatalf("lock error = %v, want errLockingUnsupported", err)
	}
}
