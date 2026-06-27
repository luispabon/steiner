package oauth

import (
	"crypto/rand"
	"crypto/sha256"
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

// ChallengeS256 computes the S256 challenge: SHA256(verifier) encoded as base64url (no padding).
func ChallengeS256(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
