//go:build unix && !aix

package oauth

import (
	"context"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestAcquireTokenLockUnsupportedFlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	lock := tokenLock{flock: func(int, int) error { return unix.EOPNOTSUPP }}
	release, err := lock.acquire(context.Background(), path)
	if err != nil {
		t.Fatalf("acquire() error = %v, want no error when flock is unsupported", err)
	}
	if release == nil {
		t.Fatal("release func is nil")
	}
	release()
}

func TestUnsupportedFlock(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "no locks available", err: unix.ENOLCK, want: true},
		{name: "operation not supported", err: unix.EOPNOTSUPP, want: true},
		{name: "lock held", err: unix.EWOULDBLOCK, want: false},
		{name: "other error", err: context.Canceled, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unsupportedFlock(tt.err); got != tt.want {
				t.Errorf("unsupportedFlock(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
