//go:build (linux || darwin || windows) && (amd64 || arm64)

package tui

import (
	"net/http"

	nativeclipboard "github.com/aymanbagabas/go-nativeclipboard"
)

// ReadClipboardImage reads image bytes from the system clipboard.
// Returns ErrClipboardNoImage if the clipboard contains no image data.
// Returns ErrImageTooLarge if the image exceeds 5MB.
func ReadClipboardImage() ([]byte, string, error) {
	data, err := nativeclipboard.Image.Read()
	if err != nil {
		return nil, "", ErrClipboardNoImage
	}
	if len(data) == 0 {
		return nil, "", ErrClipboardNoImage
	}
	if len(data) > clipboardMaxImageBytes {
		return nil, "", ErrImageTooLarge
	}
	mimeType := http.DetectContentType(data)
	return data, mimeType, nil
}

// ReadClipboardText reads text from the system clipboard.
func ReadClipboardText() (string, error) {
	data, err := nativeclipboard.Text.Read()
	if err != nil {
		return "", err
	}
	return string(data), nil
}
