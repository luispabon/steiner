package provider

import (
	"crypto/sha256"
	"encoding/hex"
)

// ImageFingerprint is the identity of one image payload a provider records as
// delivered: SHA-256 over the media type, a NUL separator, and the base64 data.
// It never exposes the payload itself. The agent records delivered fingerprints
// with the same helper, so a stateful provider can check a recorded fingerprint
// against the bytes a message still carries.
func ImageFingerprint(mediaType, data string) string {
	sum := sha256.Sum256([]byte(mediaType + "\x00" + data))
	return hex.EncodeToString(sum[:])
}
