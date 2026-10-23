package oneshot

import (
	"errors"
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

// preflightConfig returns a config whose models all run on a local provider
// except the "claude" alias, which uses claude_subscription.
func preflightConfig() config.Config {
	return config.Config{
		Providers: map[string]config.ProviderConfig{
			"local":  {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
			"claude": {Type: config.ProviderTypeClaudeSubscription},
		},
		SubAgent: config.SubAgentConfig{Enabled: true},
		Models: config.ModelsConfig{
			Definitions: map[string]config.ModelConfig{
				"local-model": {Provider: "local", ID: "local-model"},
				"claude":      {Provider: "claude", ID: "claude-sonnet"},
			},
			Effective: config.EffectiveModelAssignments{
				DefaultModel: "local-model",
				Advisor:      "local-model",
				OneShot:      map[string]string{},
				SubAgents:    map[string]string{},
			},
		},
	}
}

func TestRequireSubAgents(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		wantErr string
	}{
		{name: "enabled", enabled: true},
		{name: "disabled", enabled: false, wantErr: "oneshot unavailable: sub-agents are disabled in config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{SubAgent: config.SubAgentConfig{Enabled: tt.enabled}}
			err := RequireSubAgents(cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("RequireSubAgents() error = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("RequireSubAgents() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestRequireProviders(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*config.Config)
		wantErr bool
	}{
		{name: "local models only", mutate: func(*config.Config) {}},
		{name: "default model is claude", mutate: func(c *config.Config) { c.Models.Effective.DefaultModel = "claude" }, wantErr: true},
		{name: "advisor is claude", mutate: func(c *config.Config) { c.Models.Effective.Advisor = "claude" }, wantErr: true},
		{name: "phase model is claude", mutate: func(c *config.Config) { c.Models.Effective.OneShot = map[string]string{"review": "claude"} }, wantErr: true},
		{name: "sub-agent model is claude", mutate: func(c *config.Config) { c.Models.Effective.SubAgents = map[string]string{"explore": " claude "} }, wantErr: true},
		{name: "unknown alias is not a provider refusal", mutate: func(c *config.Config) { c.Models.Effective.Advisor = "missing" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := preflightConfig()
			tt.mutate(&cfg)
			err := RequireProviders(cfg)
			if tt.wantErr {
				if !errors.Is(err, ErrClaudeSubscription) {
					t.Fatalf("RequireProviders() error = %v, want %v", err, ErrClaudeSubscription)
				}
				return
			}
			if err != nil {
				t.Fatalf("RequireProviders() error = %v, want nil", err)
			}
		})
	}
}

func TestErrClaudeSubscriptionText(t *testing.T) {
	const want = "the claude_subscription provider cannot be used for oneshot runs; choose another provider for oneshot"
	if got := ErrClaudeSubscription.Error(); got != want {
		t.Fatalf("ErrClaudeSubscription = %q, want %q", got, want)
	}
}

func TestRequireModels(t *testing.T) {
	setPhases := func(cfg *config.Config, alias string) {
		for _, phase := range phaseOrder() {
			cfg.Models.Effective.OneShot[string(phase)] = alias
		}
	}
	tests := []struct {
		name       string
		mutate     func(*config.Config)
		wantErr    error
		wantAnyErr bool
	}{
		{name: "profile default covers phases and advisor", mutate: func(*config.Config) {}},
		{name: "empty models", mutate: func(c *config.Config) { c.Models.Effective = config.EffectiveModelAssignments{} }, wantErr: config.ErrNoModelConfigured},
		{name: "advisor missing when phases are set", mutate: func(c *config.Config) {
			c.Models.Effective.DefaultModel = ""
			c.Models.Effective.Advisor = ""
			setPhases(c, "local-model")
		}, wantErr: config.ErrNoModelConfigured},
		{name: "active orchestrator model is not a fallback", mutate: func(c *config.Config) {
			c.Models.Effective.DefaultModel = ""
			c.Models.Effective.Advisor = ""
			c.Models.Effective.ActiveOrchestratorModel = "local-model"
		}, wantErr: config.ErrNoModelConfigured},
		{name: "undefined advisor alias is not a missing model", mutate: func(c *config.Config) { c.Models.Effective.Advisor = "missing" }, wantAnyErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := preflightConfig()
			tt.mutate(&cfg)
			err := RequireModels(cfg)
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("RequireModels() error = %v, want %v", err, tt.wantErr)
				}
			case tt.wantAnyErr:
				if err == nil || errors.Is(err, config.ErrNoModelConfigured) {
					t.Fatalf("RequireModels() error = %v, want a non-sentinel error", err)
				}
			default:
				if err != nil {
					t.Fatalf("RequireModels() error = %v, want nil", err)
				}
			}
		})
	}
}

func TestModelAliases(t *testing.T) {
	cfg := preflightConfig()
	cfg.Models.Effective.DefaultModel = "default"
	cfg.Models.Effective.Advisor = "advisor"
	cfg.Models.Effective.OneShot = map[string]string{"review": "review-model"}
	cfg.Models.Effective.SubAgents = map[string]string{"explore": "explore-model"}

	got := modelAliases(cfg)
	want := []string{"default", "advisor", "review-model", "explore-model"}
	if len(got) != len(want) {
		t.Fatalf("modelAliases() = %v, want %d aliases", got, len(want))
	}
	for _, alias := range want {
		if !slices.Contains(got, alias) {
			t.Errorf("modelAliases() = %v, missing %q", got, alias)
		}
	}
}
