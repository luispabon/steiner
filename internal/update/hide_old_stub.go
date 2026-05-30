//go:build !windows

package update

// hideFile is a no-op on non-Windows platforms. The .old file removal failure
// is silently ignored on these platforms.
func hideFile(_ string) error {
	return nil
}
