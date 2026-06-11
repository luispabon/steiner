package builtin

import (
	"bytes"
	"strings"
)

// isBinary checks if content contains null bytes in the first 8KB.
func isBinary(data []byte) bool {
	checkLen := len(data)
	if checkLen > 8192 {
		checkLen = 8192
	}
	return bytes.IndexByte(data[:checkLen], 0) >= 0
}

// IsImageExtension reports whether ext (including leading dot, e.g. ".png")
// is a supported image format.
func IsImageExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}
