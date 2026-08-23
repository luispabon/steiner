package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseModelReference(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"open":       {},
			"openrouter": {},
			"local":      {},
		},
		Models: ModelsConfig{Definitions: map[string]ModelConfig{
			"alias": {Provider: "local", ID: "configured-model"},
		}},
	}
	tests := []struct {
		name      string
		ref       string
		wantProv  string
		wantModel string
		wantValid bool
	}{
		{name: "alias hit", ref: "alias", wantProv: "local", wantModel: "configured-model", wantValid: true},
		{name: "longest provider prefix", ref: "openrouter/openai/gpt-4", wantProv: "openrouter", wantModel: "openai/gpt-4", wantValid: true},
		{name: "id with slashes", ref: "local/org/model/variant", wantProv: "local", wantModel: "org/model/variant", wantValid: true},
		{name: "unknown provider prefix", ref: "missing/model", wantValid: false},
		{name: "empty reference", ref: "", wantValid: false},
		{name: "provider without id", ref: "local/", wantValid: false},
		{name: "no providers", ref: "local/model", wantValid: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cfg
			if tt.name == "no providers" {
				candidate.Providers = nil
			}
			provider, model, err := ParseModelReference(&candidate, tt.ref)
			if got := err == nil; got != tt.wantValid {
				t.Fatalf("ParseModelReference() valid = %v, want %v, err = %v", got, tt.wantValid, err)
			}
			if tt.wantValid && (provider != tt.wantProv || model != tt.wantModel) {
				t.Fatalf("ParseModelReference() = %q, %q, want %q, %q", provider, model, tt.wantProv, tt.wantModel)
			}
			if IsValidModelReference(&candidate, tt.ref) != tt.wantValid {
				t.Fatalf("IsValidModelReference() does not match expected validity")
			}
		})
	}
}

func TestParseModelReferenceAliasWins(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{"local": {}},
		Models: ModelsConfig{Definitions: map[string]ModelConfig{
			"local/model": {Provider: "local", ID: "alias-model"},
		}},
	}
	provider, model, err := ParseModelReference(&cfg, "local/model")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual([]string{provider, model}, []string{"local", "alias-model"}) {
		t.Fatalf("ParseModelReference() = %q, %q", provider, model)
	}
}

func TestParseModelReferenceErrorsAreDescriptive(t *testing.T) {
	_, _, err := ParseModelReference(&Config{}, "garbage")
	if err == nil || !strings.Contains(err.Error(), "model reference") {
		t.Fatalf("ParseModelReference() error = %v, want model reference message", err)
	}
}

func TestNewModelConfigBase(t *testing.T) {
	base := NewModelConfigBase()
	if base.Retry.MaxAttempts != 5 || base.Advanced.Limits.ContextWindow != 32768 || base.Advanced.Limits.MaxOutputTokens != 8192 {
		t.Fatalf("NewModelConfigBase() = %#v, want default retry and limits", base)
	}
}

func TestModelReferenceValidationAndOverrides(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Config)
	}{
		{
			name: "default",
			setup: func(cfg *Config) {
				cfg.Models.Default = "local/raw-model"
			},
		},
		{
			name: "advisor",
			setup: func(cfg *Config) {
				cfg.Advisor = AdvisorConfig{Enabled: true, MaxUsesPerRun: 1}
				cfg.Models.Advisor = "local/raw-model"
			},
		},
		{
			name: "sub-agent",
			setup: func(cfg *Config) {
				cfg.Models.SubAgents = map[string]string{"code": "local/raw-model"}
			},
		},
		{
			name: "oneshot",
			setup: func(cfg *Config) {
				cfg.Models.OneShot = map[string]string{"plan": "local/raw-model"}
			},
		},
		{
			name: "workflow handoff",
			setup: func(cfg *Config) {
				cfg.Models.WorkflowHandoff = map[string]string{"review": "local/raw-model"}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBase()
			tt.setup(&cfg)
			if err := validate(cfg); err != nil {
				t.Fatalf("validate() error = %v, want nil", err)
			}
		})
	}

	cfg := defaultConfig()
	applyCLIOverrides(&cfg, CLIOverrides{Model: "local/raw-model"})
	if cfg.Models.Default != "local/raw-model" {
		t.Fatalf("CLI model override = %q, want raw reference", cfg.Models.Default)
	}
	applyCLIOverrides(&cfg, CLIOverrides{Model: "garbage"})
	if cfg.Models.Default != "local/raw-model" {
		t.Fatalf("invalid CLI model override changed default to %q", cfg.Models.Default)
	}
	if err := applyEnvOverrides(&cfg, map[string]string{"STEINER_MODEL": "local/env-model"}); err != nil {
		t.Fatalf("applyEnvOverrides() error = %v", err)
	}
	if cfg.Models.Default != "local/env-model" {
		t.Fatalf("env model override = %q, want raw reference", cfg.Models.Default)
	}
}
