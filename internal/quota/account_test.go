package quota

import (
	"regexp"
	"testing"
)

func TestAPIKeyAccountKey(t *testing.T) {
	const apiKey = "sk-secret-value"
	base := APIKeyAccountKey("openai", "https://api.example.com", apiKey)

	shape := regexp.MustCompile(`^openai:[0-9a-f]{16}$`)
	if !shape.MatchString(base) {
		t.Errorf("key %q does not match providerType:16 hex characters", base)
	}

	tests := []struct {
		name         string
		providerType string
		baseURL      string
		apiKey       string
		wantSame     bool
	}{
		{"same inputs", "openai", "https://api.example.com", apiKey, true},
		{"different api key", "openai", "https://api.example.com", "sk-other", false},
		{"different base url", "openai", "https://other.example.com", apiKey, false},
		{"different provider type", "anthropic", "https://api.example.com", apiKey, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := APIKeyAccountKey(tt.providerType, tt.baseURL, tt.apiKey)
			if (got == base) != tt.wantSame {
				t.Errorf("key = %q, same as base %q: %v, want %v", got, base, got == base, tt.wantSame)
			}
		})
	}
}

func TestAPIKeyAccountKeyOmitsRawKey(t *testing.T) {
	const apiKey = "sk-secret-value"
	if key := APIKeyAccountKey("openai", "https://api.example.com", apiKey); regexp.MustCompile(`sk-secret`).MatchString(key) {
		t.Errorf("account key %q leaks the raw API key", key)
	}
}
