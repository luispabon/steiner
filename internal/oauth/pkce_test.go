package oauth

import (
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
