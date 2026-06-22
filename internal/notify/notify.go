package notify

import (
	"context"
	"fmt"
	"time"
)

// Options holds configuration for the notification service.
type Options struct {
	Enabled  bool
	Duration time.Duration
	AppName  string
}

// Notification is the payload sent to the desktop notification system.
type Notification struct {
	Project string
	Branch  string
	Reason  string
}

// driver is the pluggable interface for platform-specific notification delivery.
type driver interface {
	notify(ctx context.Context, n Notification, d time.Duration) error
	available() (bool, string)
}

// Service delivers desktop notifications.
type Service struct {
	opts Options
	drv  driver
}

// New creates a new notification Service from the given Options.
func New(opts Options) *Service {
	if !opts.Enabled {
		return &Service{
			opts: opts,
			drv:  &noopDriver{},
		}
	}
	return &Service{
		opts: opts,
		drv:  newDriver(opts),
	}
}

// Notify sends a notification. It is a no-op if the service is disabled or the driver is unavailable.
func (s *Service) Notify(ctx context.Context, n Notification) error {
	return s.drv.notify(ctx, n, s.opts.Duration)
}

// Availability reports whether the service can deliver notifications.
func (s *Service) Availability() (bool, string) {
	return s.drv.available()
}

// notificationTitle composes the notification title from the project name.
func notificationTitle(n Notification) string {
	return fmt.Sprintf("steiner — %s", n.Project)
}

// notificationBody composes the notification body from the reason and branch.
func notificationBody(n Notification) string {
	return fmt.Sprintf("%s\n%s", n.Reason, n.Branch)
}

// noopDriver is a stub driver used when notifications are disabled.
type noopDriver struct{}

func (d *noopDriver) notify(_ context.Context, _ Notification, _ time.Duration) error {
	return nil
}

func (d *noopDriver) available() (bool, string) {
	return false, "desktop notifications are disabled"
}
