package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateVerifier(t *testing.T) {
	v, err := GenerateVerifier()
	if err != nil {
		t.Fatalf("GenerateVerifier() error = %v", err)
	}

	if len(v) != 43 {
		t.Errorf("verifier length = %d, want 43", len(v))
	}

	if strings.Contains(v, "+") || strings.Contains(v, "/") || strings.Contains(v, "=") {
		t.Errorf("verifier contains invalid chars: %s", v)
	}

	decoded, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		t.Errorf("verifier is not valid base64url: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("decoded verifier length = %d, want 32", len(decoded))
	}
}

func TestChallengeS256(t *testing.T) {
	verifier := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	got := ChallengeS256(verifier)

	expectedHash := sha256.Sum256([]byte(verifier))
	expectedB64 := base64.RawURLEncoding.EncodeToString(expectedHash[:])
	if got != expectedB64 {
		t.Errorf("ChallengeS256() = %q, want %q", got, expectedB64)
	}

	if len(got) != 43 {
		t.Errorf("challenge length = %d, want 43", len(got))
	}

	if strings.Contains(got, "+") || strings.Contains(got, "/") || strings.Contains(got, "=") {
		t.Errorf("challenge contains invalid chars: %s", got)
	}
}
