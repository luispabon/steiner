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
	const forbidden = `providers["claude"]: claude_subscription takes no base_url, api_key, api_key_env or headers — it uses your claude CLI login`
	tests := []struct {
		name     string
		provider ProviderConfig
		wantErr  bool
	}{
		{
			name:     "type only is valid",
			provider: ProviderConfig{Type: ProviderTypeClaudeSubscription},
		},
		{
			name:     "base_url is rejected",
			provider: ProviderConfig{Type: ProviderTypeClaudeSubscription, BaseURL: "https://example.invalid"},
			wantErr:  true,
		},
		{
			name:     "api_key is rejected",
			provider: ProviderConfig{Type: ProviderTypeClaudeSubscription, APIKey: "sk-test"},
			wantErr:  true,
		},
		{
			name:     "api_key_env is rejected",
			provider: ProviderConfig{Type: ProviderTypeClaudeSubscription, APIKeyEnv: "ANTHROPIC_API_KEY"},
			wantErr:  true,
		},
		{
			name:     "headers are rejected",
			provider: ProviderConfig{Type: ProviderTypeClaudeSubscription, Headers: map[string]string{"x-test": "1"}},
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var problems []string
			validateProvidersConfig(&problems, map[string]ProviderConfig{"claude": tt.provider})
			joined := strings.Join(problems, "; ")
			if tt.wantErr {
				if !strings.Contains(joined, forbidden) {
					t.Fatalf("problems = %q, want to contain %q", joined, forbidden)
				}
				return
			}
			if joined != "" {
				t.Fatalf("problems = %q, want none", joined)
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
