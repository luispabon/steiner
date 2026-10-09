package config

import (
	"strings"
	"testing"
)

func TestClaudeSubscriptionNeedsNoBaseURLCredential(t *testing.T) {
	if providerNeedsBaseURL(ProviderTypeClaudeSubscription) {
		t.Error("providerNeedsBaseURL(claude_subscription) = true, want false")
	}
	if providerNeedsCredential(ProviderTypeClaudeSubscription) {
		t.Error("providerNeedsCredential(claude_subscription) = true, want false")
	}
}

func TestValidateProvidersConfigClaudeSubscription(t *testing.T) {
	tests := []struct {
		name     string
		provider ProviderConfig
		// wantFields is the field list the problem names; empty means valid.
		wantFields string
	}{
		{
			name:     "type only is valid",
			provider: ProviderConfig{Type: ProviderTypeClaudeSubscription},
		},
		{
			name:     "explicit zero timeout is valid",
			provider: ProviderConfig{Type: ProviderTypeClaudeSubscription, Timeout: MustDuration("0s")},
		},
		{
			name:       "base_url is rejected",
			provider:   ProviderConfig{Type: ProviderTypeClaudeSubscription, BaseURL: "https://example.invalid"},
			wantFields: "base_url",
		},
		{
			name:       "api_key is rejected",
			provider:   ProviderConfig{Type: ProviderTypeClaudeSubscription, APIKey: "sk-test"},
			wantFields: "api_key",
		},
		{
			name:       "api_key_env is rejected",
			provider:   ProviderConfig{Type: ProviderTypeClaudeSubscription, APIKeyEnv: "ANTHROPIC_API_KEY"},
			wantFields: "api_key_env",
		},
		{
			name:       "headers are rejected",
			provider:   ProviderConfig{Type: ProviderTypeClaudeSubscription, Headers: map[string]string{"x-test": "1"}},
			wantFields: "headers",
		},
		{
			name:       "timeout is rejected",
			provider:   ProviderConfig{Type: ProviderTypeClaudeSubscription, Timeout: MustDuration("45s")},
			wantFields: "timeout",
		},
		{
			name:       "every rejected field is named in order",
			provider:   ProviderConfig{Type: ProviderTypeClaudeSubscription, BaseURL: "https://example.invalid", APIKey: "sk-test", APIKeyEnv: "ANTHROPIC_API_KEY", Headers: map[string]string{"x-test": "1"}, Timeout: MustDuration("45s")},
			wantFields: "base_url, api_key, api_key_env, headers, timeout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var problems []string
			validateProvidersConfig(&problems, map[string]ProviderConfig{"claude": tt.provider})
			joined := strings.Join(problems, "; ")
			if tt.wantFields == "" {
				if joined != "" {
					t.Fatalf("problems = %q, want none", joined)
				}
				return
			}
			want := `providers["claude"]: claude_subscription takes no ` + tt.wantFields + ` — it uses your claude CLI login`
			if joined != want {
				t.Fatalf("problems = %q, want %q", joined, want)
			}
		})
	}
}

func TestValidateAcceptsClaudeSubscriptionProvider(t *testing.T) {
	cfg := validBase()
	cfg.Providers = map[string]ProviderConfig{
		"claude": {Type: ProviderTypeClaudeSubscription},
	}
	model := cfg.Models.Definitions["default"]
	model.Provider = "claude"
	cfg.Models.Definitions["default"] = model

	if err := validate(cfg, ""); err != nil {
		t.Fatalf("validate() error = %v, want nil", err)
	}

	cfg.Providers["claude"] = ProviderConfig{Type: ProviderTypeClaudeSubscription, APIKey: "sk-test"}
	if err := validate(cfg, ""); err == nil || !strings.Contains(err.Error(), "claude_subscription takes no") {
		t.Fatalf("validate() error = %v, want claude_subscription problem", err)
	}
}
