//go:build linux

package notify

// newDriver returns a driver implementation for the current platform.
func newDriver(opts Options) driver {
	// TODO(step-3): implement Linux desktop notification driver
	return &unsupportedDriver{}
}
