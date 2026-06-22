//go:build !linux

package notify

// newDriver returns a driver implementation for the current platform.
func newDriver(_ Options) driver {
	return &stubDriver{reason: "desktop notifications are not supported on this platform"}
}
