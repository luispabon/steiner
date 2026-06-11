//go:build !(linux || darwin || windows) || !(amd64 || arm64)

package tui

// ReadClipboardImage is not supported on this platform.
// Always returns ErrClipboardUnsupported.
func ReadClipboardImage() ([]byte, string, error) {
	return nil, "", ErrClipboardUnsupported
}

// ReadClipboardText is not supported on this platform.
// Always returns ErrClipboardUnsupported.
func ReadClipboardText() (string, error) {
	return "", ErrClipboardUnsupported
}
