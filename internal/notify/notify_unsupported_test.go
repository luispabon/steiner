//go:build !linux

package notify

import (
	"context"
	"testing"
	"time"
)

func TestUnsupportedDriverAvailability(t *testing.T) {
	drv := &unsupportedDriver{}

	avail, msg := drv.available()
	if avail {
		t.Error("Availability should return false for unsupported driver")
	}
	if msg != "desktop notifications are not supported on this platform" {
		t.Errorf("Availability message = %q, expected %q", msg, "desktop notifications are not supported on this platform")
	}
}

func TestUnsupportedDriverNotify(t *testing.T) {
	drv := &unsupportedDriver{}
	ctx := context.Background()
	n := Notification{
		Project: "test",
		Branch:  "main",
		Reason:  "test",
	}

	err := drv.notify(ctx, n, 5*time.Second)
	if err != nil {
		t.Errorf("notify returned error: %v, expected nil", err)
	}
}
