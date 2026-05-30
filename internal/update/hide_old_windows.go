//go:build windows

package update

import (
	"syscall"
)

// hideFile sets the FILE_ATTRIBUTE_HIDDEN flag on the file at path, making it
// invisible in normal directory listings on Windows.
func hideFile(path string) error {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return syscall.SetFileAttributes(pathPtr, syscall.FILE_ATTRIBUTE_HIDDEN)
}
