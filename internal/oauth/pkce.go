package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// GenerateVerifier creates a PKCE verifier: 32 random bytes encoded as base64url (no padding).
func GenerateVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
