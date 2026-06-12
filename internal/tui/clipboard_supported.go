//go:build (linux || darwin || windows) && (amd64 || arm64)

package tui

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"

	nativeclipboard "github.com/aymanbagabas/go-nativeclipboard"
)

// ReadClipboardImage reads image bytes from the system clipboard.
// Returns ErrClipboardNoImage if the clipboard contains no image data.
// Returns ErrImageTooLarge if the image exceeds 5MB.
func ReadClipboardImage() ([]byte, string, error) {
	// Wayland clipboard is separate from X11. go-nativeclipboard reads X11
	// only; XWayland does not bridge image MIME types Wayland → X11.
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		data, mimeType, err := readImageViaWlPaste()
		if err == nil {
			return data, mimeType, nil
		}
		if errors.Is(err, ErrImageTooLarge) {
			return nil, "", err
		}
	}

	// X11 fallback (pure X11 sessions or XWayland with clipboard bridging).
	data, err := nativeclipboard.Image.Read()
	if err != nil || len(data) == 0 {
		return nil, "", ErrClipboardNoImage
	}
	if len(data) > clipboardMaxImageBytes {
		return nil, "", ErrImageTooLarge
	}
	return data, http.DetectContentType(data), nil
}

// ReadClipboardText reads text from the system clipboard.
func ReadClipboardText() (string, error) {
	data, err := nativeclipboard.Text.Read()
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// readImageViaWlPaste reads an image from the Wayland clipboard using wl-paste.
// Returns ErrClipboardNoImage when no image MIME type is offered.
func readImageViaWlPaste() ([]byte, string, error) {
	out, err := exec.CommandContext(context.Background(), "wl-paste", "--list-types").Output()
	if err != nil {
		return nil, "", ErrClipboardNoImage
	}
	available := string(out)
	for _, mime := range []string{"image/png", "image/jpeg", "image/webp", "image/gif"} {
		if !strings.Contains(available, mime) {
			continue
		}
		data, err := exec.CommandContext(context.Background(), "wl-paste", "--type", mime, "--no-newline").Output()
		if err != nil || len(data) == 0 {
			continue
		}
		if len(data) > clipboardMaxImageBytes {
			return nil, "", ErrImageTooLarge
		}
		return data, mime, nil
	}
	return nil, "", ErrClipboardNoImage
}
