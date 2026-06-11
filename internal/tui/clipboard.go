package tui

import "errors"

var (
	// ErrClipboardNoImage is returned when the clipboard contains no image data.
	ErrClipboardNoImage = errors.New("clipboard: no image data")
	// ErrClipboardUnsupported is returned when clipboard access is not supported on the current platform.
	ErrClipboardUnsupported = errors.New("clipboard: not supported on this platform")
	// ErrImageTooLarge is returned when the clipboard image exceeds the maximum allowed size.
	ErrImageTooLarge = errors.New("clipboard: image too large (max 5MB)")
)

const clipboardMaxImageBytes = 5 * 1024 * 1024
