package config

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const providerOnlyLocalConfig = `providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
`

func TestLoadZeroConfigHasEmptyModelMaps(t *testing.T) {
	tempDir := t.TempDir()
	cfg, err := loadTrusted(LoadOptions{
		HomeDir:    filepath.Join(tempDir, "home"),
		WorkingDir: tempDir,
		Env:        map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if cfg.Providers == nil || len(cfg.Providers) != 0 {
		t.Errorf("Providers = %#v, want empty non-nil map", cfg.Providers)
	}
	if cfg.Models.Definitions == nil || len(cfg.Models.Definitions) != 0 {
		t.Errorf("Models.Definitions = %#v, want empty non-nil map", cfg.Models.Definitions)
	}
	if cfg.Models.Profiles == nil || len(cfg.Models.Profiles) != 0 {
		t.Errorf("Models.Profiles = %#v, want empty non-nil map", cfg.Models.Profiles)
	}
	if got := cfg.Models.Effective; !reflect.DeepEqual(got, EffectiveModelAssignments{}) {
		t.Errorf("Models.Effective = %#v, want zero value (unselected)", got)
	}
}

func TestLoadNamedOnlyProfilesLeaveSelectionUnset(t *testing.T) {
	cfg := loadProfileTestConfig(t, providerOnlyLocalConfig+`models:
  definitions:
    fast:
      provider: local
      id: fast
  profiles:
    fast:
      default_model: fast
`)

	if got := cfg.Models.Effective; !reflect.DeepEqual(got, EffectiveModelAssignments{}) {
		t.Fatalf("Models.Effective = %#v, want zero value (unselected)", got)
	}
}

func TestLoadExplicitMissingProfileErrors(t *testing.T) {
	const contents = providerOnlyLocalConfig + `models:
  definitions:
    fast:
      provider: local
      id: fast
  profiles:
    fast:
      default_model: fast
`
	tests := []struct {
		name    string
		profile string
	}{
		{name: "named profile not declared", profile: "missing"},
		{name: "default not declared", profile: "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadProfileTestConfigResult(t, contents, CLIOverrides{Profile: tt.profile}, nil)
			if err == nil || !strings.Contains(err.Error(), "profile is not defined") {
				t.Fatalf("Load() error = %v, want profile is not defined", err)
			}
		})
	}
}

func TestLoadProviderOnlyWithRawModelSetsOnlyOrchestrator(t *testing.T) {
	cfg := loadProfileTestConfig(t, providerOnlyLocalConfig, CLIOverrides{Model: "local/raw-model"})

	want := EffectiveModelAssignments{ActiveOrchestratorModel: "local/raw-model"}
	if got := cfg.Models.Effective; !reflect.DeepEqual(got, want) {
		t.Fatalf("Models.Effective = %#v, want %#v", got, want)
	}
}

func TestLoadNewAliasNeverInheritsFromRedefinedDefault(t *testing.T) {
	const contents = providerOnlyLocalConfig + `models:
  profiles:
    default:
      default_model: custom
  definitions:
    default:
      provider: local
      id: qwen3-35b-a3b
      params:
        temperature: 0.1
      prompts:
        system: default system
      vision: true
      retry:
        max_attempts: 9
      advanced:
        limits:
          context_window: 32768
          max_output_tokens: 8192
        transport: anthropic
    custom:
      provider: local
      id: custom-backend
`
	for i := range 50 {
		cfg := loadProfileTestConfig(t, contents)
		custom := cfg.Models.Definitions["custom"]
		if custom.Provider != "local" || custom.ID != "custom-backend" {
			t.Fatalf("iteration %d: models[custom] identity = %q/%q, want local/custom-backend", i, custom.Provider, custom.ID)
		}
		if !reflect.DeepEqual(custom.Retry, NewModelConfigBase().Retry) {
			t.Fatalf("iteration %d: models[custom].retry = %#v, want base retry", i, custom.Retry)
		}
		if len(custom.Params) != 0 || custom.Prompts != (ModelPrompts{}) || custom.Vision != nil {
			t.Fatalf("iteration %d: models[custom] inherited params, prompts, or vision: %#v", i, custom)
		}
		if custom.Advanced.Transport != "" || custom.Advanced.Limits != (AdvancedLimitsConfig{}) {
			t.Fatalf("iteration %d: models[custom].advanced inherited transport or limits: %#v", i, custom.Advanced)
		}
	}
}

func TestRequireModelReference(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"local": {Type: ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: ModelsConfig{
			Definitions: map[string]ModelConfig{
				"fast": {Provider: "local", ID: "fast-model"},
			},
		},
	}
	tests := []struct {
		name     string
		ref      string
		sentinel bool
		wantErr  bool
	}{
		{name: "empty", ref: "", sentinel: true, wantErr: true},
		{name: "whitespace only", ref: " \t ", sentinel: true, wantErr: true},
		{name: "not an alias or reference", ref: "garbage", wantErr: true},
		{name: "unknown provider", ref: "nope/model", wantErr: true},
		{name: "provider without model id", ref: "local/", wantErr: true},
		{name: "provider model reference", ref: "local/raw-model"},
		{name: "defined alias", ref: "fast"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RequireModelReference(cfg, tt.ref)
			switch {
			case tt.sentinel:
				if !errors.Is(err, ErrNoModelConfigured) {
					t.Fatalf("RequireModelReference(%q) error = %v, want ErrNoModelConfigured", tt.ref, err)
				}
			case tt.wantErr:
				if err == nil || errors.Is(err, ErrNoModelConfigured) {
					t.Fatalf("RequireModelReference(%q) error = %v, want descriptive invalid reference error", tt.ref, err)
				}
			default:
				if err != nil {
					t.Fatalf("RequireModelReference(%q) error = %v, want nil", tt.ref, err)
				}
			}
		})
	}
}
