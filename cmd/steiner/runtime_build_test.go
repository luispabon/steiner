package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

func TestBuildRuntimeProviderFactoryDispatchesByResolvedProviderType(t *testing.T) {
	oldNewScheduler := newScheduler
	oldNewOpenAICompat := newOpenAICompat
	oldNewAnthropic := newAnthropic
	t.Cleanup(func() {
		newScheduler = oldNewScheduler
		newOpenAICompat = oldNewOpenAICompat
		newAnthropic = oldNewAnthropic
	})

	const wantParallelism = 7
	httpClient := &http.Client{}
	baseRetry := provider.RetryConfig{
		Enabled:        true,
		MaxAttempts:    4,
		InitialBackoff: 250 * time.Millisecond,
		MaxBackoff:     5 * time.Second,
		RetryAfterMax:  30 * time.Second,
	}

	type capture struct {
		openAICompatCalls int
		anthropicCalls    int
		openAICompatCfg   provider.OpenAICompatConfig
		anthropicCfg      provider.OpenAICompatConfig
	}

	runFactory := func(t *testing.T, rm provider.ResolvedModel, wantErr string, wantProviderKind string) capture {
		t.Helper()

		var got capture
		newScheduler = func(parallelism int) (*provider.Scheduler, error) {
			if parallelism != wantParallelism {
				t.Fatalf("scheduler parallelism = %d, want %d", parallelism, wantParallelism)
			}
			return provider.NewScheduler(parallelism)
		}
		newOpenAICompat = func(cfg provider.OpenAICompatConfig) (provider.Provider, error) {
			got.openAICompatCalls++
			got.openAICompatCfg = cfg
			return &fakeProvider{}, nil
		}
		newAnthropic = func(cfg provider.OpenAICompatConfig) (provider.Provider, error) {
			got.anthropicCalls++
			got.anthropicCfg = cfg
			return &fakeProvider{}, nil
		}

		factory, err := buildRuntimeProviderFactory(config.Config{
			Scheduler: config.SchedulerConfig{Parallelism: wantParallelism},
		}, httpClient, nil)
		if err != nil {
			t.Fatalf("buildRuntimeProviderFactory() error = %v", err)
		}

		gotProvider, err := factory(rm)
		if wantErr != "" {
			if err == nil {
				t.Fatalf("factory() error = nil, want %q", wantErr)
			}
			if err.Error() != wantErr {
				t.Fatalf("factory() error = %q, want %q", err.Error(), wantErr)
			}
			if gotProvider != nil {
				t.Fatalf("factory() provider = %T, want nil", gotProvider)
			}
			return got
		}

		if err != nil {
			t.Fatalf("factory() error = %v", err)
		}
		if gotProvider == nil {
			t.Fatal("factory() provider = nil, want provider")
		}
		switch wantProviderKind {
		case "openai_compat":
			if got.openAICompatCalls != 1 {
				t.Fatalf("openai compat constructor calls = %d, want 1", got.openAICompatCalls)
			}
			if got.anthropicCalls != 0 {
				t.Fatalf("anthropic constructor calls = %d, want 0", got.anthropicCalls)
			}
		case "anthropic":
			if got.anthropicCalls != 1 {
				t.Fatalf("anthropic constructor calls = %d, want 1", got.anthropicCalls)
			}
			if got.openAICompatCalls != 0 {
				t.Fatalf("openai compat constructor calls = %d, want 0", got.openAICompatCalls)
			}
		default:
			t.Fatalf("unknown wantProviderKind %q", wantProviderKind)
		}
		return got
	}

	compatRM := provider.ResolvedModel{
		Alias:                 "compat",
		ProviderConfig:        config.ProviderConfig{Type: config.ProviderTypeOpenAICompat, BaseURL: "http://compat.example/v1", APIKey: "compat-key", Headers: map[string]string{"X-Test": "compat"}, Timeout: config.MustDuration("45s")},
		BackendModelID:        "compat-backend",
		EffectiveProviderType: config.ProviderTypeOpenAICompat,
		Retry: config.RetryConfig{
			Enabled:        baseRetry.Enabled,
			MaxAttempts:    baseRetry.MaxAttempts,
			InitialBackoff: config.MustDuration("250ms"),
			MaxBackoff:     config.MustDuration("5s"),
			RetryAfterMax:  config.MustDuration("30s"),
		},
	}
	gotCompat := runFactory(t, compatRM, "", "openai_compat")
	if got := gotCompat.openAICompatCfg; got.BaseURL != compatRM.ProviderConfig.BaseURL {
		t.Fatalf("openai compat base URL = %q, want %q", got.BaseURL, compatRM.ProviderConfig.BaseURL)
	}
	if got := gotCompat.openAICompatCfg.APIKey; got != compatRM.ProviderConfig.APIKey {
		t.Fatalf("openai compat API key = %q, want %q", got, compatRM.ProviderConfig.APIKey)
	}
	if got := gotCompat.openAICompatCfg.Headers["X-Test"]; got != "compat" {
		t.Fatalf("openai compat headers = %#v, want X-Test=compat", gotCompat.openAICompatCfg.Headers)
	}
	if got := gotCompat.openAICompatCfg.Model; got != compatRM.BackendModelID {
		t.Fatalf("openai compat model = %q, want %q", got, compatRM.BackendModelID)
	}
	if got := gotCompat.openAICompatCfg.Timeout; got != 45*time.Second {
		t.Fatalf("openai compat timeout = %v, want 45s", got)
	}
	if got := gotCompat.openAICompatCfg.Retry; got != baseRetry {
		t.Fatalf("openai compat retry = %#v, want %#v", got, baseRetry)
	}
	if got := gotCompat.openAICompatCfg.HTTPClient; got != httpClient {
		t.Fatalf("openai compat HTTP client = %p, want %p", got, httpClient)
	}
	if got := gotCompat.openAICompatCfg.ProviderType; got != string(config.ProviderTypeOpenAICompat) {
		t.Fatalf("openai compat provider type = %q, want %q", got, config.ProviderTypeOpenAICompat)
	}

	anthropicRM := provider.ResolvedModel{
		Alias:                 "anthropic",
		ProviderConfig:        config.ProviderConfig{Type: config.ProviderTypeOpenAICompat, BaseURL: "http://anthropic.example/v1", APIKey: "anthropic-key", Headers: map[string]string{"X-Test": "anthropic"}, Timeout: config.MustDuration("30s")},
		BackendModelID:        "anthropic-backend",
		EffectiveProviderType: config.ProviderTypeAnthropic,
		Retry: config.RetryConfig{
			Enabled:        baseRetry.Enabled,
			MaxAttempts:    baseRetry.MaxAttempts,
			InitialBackoff: config.MustDuration("250ms"),
			MaxBackoff:     config.MustDuration("5s"),
			RetryAfterMax:  config.MustDuration("30s"),
		},
	}
	gotAnthropic := runFactory(t, anthropicRM, "", "anthropic")
	if gotAnthropic.openAICompatCalls != 0 {
		t.Fatalf("anthropic should not call openai compat constructor, got %d calls", gotAnthropic.openAICompatCalls)
	}
	if got := gotAnthropic.anthropicCfg; got.BaseURL != anthropicRM.ProviderConfig.BaseURL {
		t.Fatalf("anthropic base URL = %q, want %q", got.BaseURL, anthropicRM.ProviderConfig.BaseURL)
	}
	if got := gotAnthropic.anthropicCfg.APIKey; got != anthropicRM.ProviderConfig.APIKey {
		t.Fatalf("anthropic API key = %q, want %q", got, anthropicRM.ProviderConfig.APIKey)
	}
	if got := gotAnthropic.anthropicCfg.Headers["X-Test"]; got != "anthropic" {
		t.Fatalf("anthropic headers = %#v, want X-Test=anthropic", gotAnthropic.anthropicCfg.Headers)
	}
	if got := gotAnthropic.anthropicCfg.Model; got != anthropicRM.BackendModelID {
		t.Fatalf("anthropic model = %q, want %q", got, anthropicRM.BackendModelID)
	}
	if got := gotAnthropic.anthropicCfg.Timeout; got != 30*time.Second {
		t.Fatalf("anthropic timeout = %v, want 30s", got)
	}
	if got := gotAnthropic.anthropicCfg.Retry; got != baseRetry {
		t.Fatalf("anthropic retry = %#v, want %#v", got, baseRetry)
	}
	if got := gotAnthropic.anthropicCfg.HTTPClient; got != httpClient {
		t.Fatalf("anthropic HTTP client = %p, want %p", got, httpClient)
	}
	if got := gotAnthropic.anthropicCfg.ProviderType; got != string(config.ProviderTypeAnthropic) {
		t.Fatalf("anthropic provider type = %q, want %q", got, config.ProviderTypeAnthropic)
	}

	legacyAnthropicRM := provider.ResolvedModel{
		Alias:          "legacy-anthropic",
		ProviderConfig: config.ProviderConfig{Type: config.ProviderTypeAnthropic, BaseURL: "http://anthropic.example/v1", APIKey: "anthropic-key", Headers: map[string]string{"X-Test": "legacy"}, Timeout: config.MustDuration("30s")},
		BackendModelID: "anthropic-backend",
		Retry: config.RetryConfig{
			Enabled:        baseRetry.Enabled,
			MaxAttempts:    baseRetry.MaxAttempts,
			InitialBackoff: config.MustDuration("250ms"),
			MaxBackoff:     config.MustDuration("5s"),
			RetryAfterMax:  config.MustDuration("30s"),
		},
	}
	gotLegacyAnthropic := runFactory(t, legacyAnthropicRM, "", "anthropic")
	if gotLegacyAnthropic.anthropicCalls != 1 {
		t.Fatalf("legacy anthropic constructor calls = %d, want 1", gotLegacyAnthropic.anthropicCalls)
	}

	unsupportedRM := provider.ResolvedModel{
		Alias:                 "gemini",
		ProviderConfig:        config.ProviderConfig{Type: config.ProviderTypeGemini},
		BackendModelID:        "gemini-backend",
		EffectiveProviderType: config.ProviderTypeGemini,
	}
	runFactory(t, unsupportedRM, `provider type "gemini" is not implemented by the runtime provider factory`, "")
}

func TestBuildRuntimeProviderFactoryCodexMissingToken(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	factory, err := buildRuntimeProviderFactory(config.Config{
		Scheduler: config.SchedulerConfig{Parallelism: 1},
	}, &http.Client{}, nil)
	if err != nil {
		t.Fatalf("buildRuntimeProviderFactory() error = %v", err)
	}

	codexRM := provider.ResolvedModel{
		Alias:                 "codex",
		ProviderConfig:        config.ProviderConfig{Type: config.ProviderTypeCodex},
		BackendModelID:        "codex-default",
		EffectiveProviderType: config.ProviderTypeCodex,
	}
	_, err = factory(codexRM)
	if err == nil {
		t.Fatal("factory() error = nil, want actionable error")
	}
	want := "codex provider requires authentication — run 'steiner login codex' first"
	if err.Error() != want {
		t.Fatalf("factory() error = %q, want %q", err.Error(), want)
	}
}
