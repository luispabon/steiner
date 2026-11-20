package provider

import (
	"crypto/sha256"
	"encoding/hex"
)

// ImageFingerprint is the identity of one image payload a provider records as
// delivered: SHA-256 over the media type, a NUL separator, and the base64 data.
// It never exposes the payload itself, so a checkpoint can record what a
// process received without holding image bytes.
func ImageFingerprint(mediaType, data string) string {
	sum := sha256.Sum256([]byte(mediaType + "\x00" + data))
	return hex.EncodeToString(sum[:])
}
