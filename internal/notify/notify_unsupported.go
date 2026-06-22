//go:build !linux

package notify

import (
	"context"
	"time"
)

// newDriver returns a driver implementation for the current platform.
func newDriver(_ Options) driver {
	return &unsupportedDriver{}
}

type unsupportedDriver struct{}

func (d *unsupportedDriver) notify(_ context.Context, _ Notification, _ time.Duration) error {
	return nil
}

func (d *unsupportedDriver) available() (bool, string) {
	return false, "desktop notifications are not supported on this platform"
}
