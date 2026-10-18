package quota

import "testing"

func TestAPIKeyAccountKey(t *testing.T) {
	tests := []struct {
		alias string
		want  string
	}{
		{"main", "provider:main"},
		{"litellm-b", "provider:litellm-b"},
	}
	for _, tt := range tests {
		if got := APIKeyAccountKey(tt.alias); got != tt.want {
			t.Errorf("APIKeyAccountKey(%q) = %q, want %q", tt.alias, got, tt.want)
		}
	}
}

func TestIsLegacyAPIKeyAccountKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want bool
	}{
		{"opencode_go fingerprint", "opencode_go:0123456789abcdef", true},
		{"openrouter fingerprint", "openrouter:0123456789abcdef", true},
		{"litellm fingerprint", "litellm:fedcba9876543210", true},
		{"alias key", APIKeyAccountKey("main"), false},
		{"alias key shaped like a fingerprint", "provider:0123456789abcdef", false},
		{"codex account", "codex:acct-1", false},
		{"claude subscription", "claude_subscription", false},
		{"provider type not in the legacy list", "openai:0123456789abcdef", false},
		{"fifteen hex characters", "openrouter:0123456789abcde", false},
		{"seventeen hex characters", "openrouter:0123456789abcdef0", false},
		{"uppercase hex", "openrouter:0123456789ABCDEF", false},
		{"non-hex character", "openrouter:0123456789abcdeg", false},
		{"no separator", "openrouter0123456789abcdef", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLegacyAPIKeyAccountKey(tt.key); got != tt.want {
				t.Errorf("IsLegacyAPIKeyAccountKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}
