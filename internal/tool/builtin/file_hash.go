package builtin

import (
	"crypto/sha256"
	"encoding/hex"
)

// fileContentHash returns a short hex digest of the file content for staleness checks.
func fileContentHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}
