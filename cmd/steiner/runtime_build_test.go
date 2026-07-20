package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

func TestBuildRuntimeProviderFactoryDispatchesByResolvedProviderType(t *testing.T) {
	oldNewScheduler := newScheduler
	oldNewOpenAICompat := newOpenAICompat
	oldNewAnthropic := newAnthropic
	oldNewCodexResponses := newCodexResponses
	t.Cleanup(func() {
		newScheduler = oldNewScheduler
		newOpenAICompat = oldNewOpenAICompat
		newAnthropic = oldNewAnthropic
		newCodexResponses = oldNewCodexResponses
	})

	const wantParallelism = 7
	httpClient := &http.Client{}
	streamErrorLog, err := provider.NewStreamErrorLogger(filepath.Join(t.TempDir(), "stream-errors.log"))
	if err != nil {
		t.Fatalf("NewStreamErrorLogger() error = %v", err)
	}
	t.Cleanup(func() {
		_ = streamErrorLog.Close()
	})
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
		codexCalls        int
		openAICompatCfg   provider.ClientConfig
		anthropicCfg      provider.ClientConfig
		codexCfg          provider.ClientConfig
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
		newOpenAICompat = func(cfg provider.ClientConfig) (provider.Provider, error) {
			got.openAICompatCalls++
			got.openAICompatCfg = cfg
			return &fakeProvider{}, nil
		}
		newAnthropic = func(cfg provider.ClientConfig) (provider.Provider, error) {
			got.anthropicCalls++
			got.anthropicCfg = cfg
			return &fakeProvider{}, nil
		}
		newCodexResponses = func(cfg provider.ClientConfig) (provider.Provider, error) {
			got.codexCalls++
			got.codexCfg = cfg
			return &fakeProvider{}, nil
		}

		factory, err := buildRuntimeProviderFactory(config.Config{
			Scheduler: config.SchedulerConfig{Parallelism: wantParallelism},
		}, httpClient, streamErrorLog)
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
		case "codex":
			if got.codexCalls != 1 {
				t.Fatalf("codex constructor calls = %d, want 1", got.codexCalls)
			}
			if got.openAICompatCalls != 0 {
				t.Fatalf("openai compat constructor calls = %d, want 0", got.openAICompatCalls)
			}
			if got.anthropicCalls != 0 {
				t.Fatalf("anthropic constructor calls = %d, want 0", got.anthropicCalls)
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
	if got := gotCompat.openAICompatCfg.StreamErrorLog; got != streamErrorLog {
		t.Fatalf("openai compat stream error log = %p, want %p", got, streamErrorLog)
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
	if got := gotAnthropic.anthropicCfg.StreamErrorLog; got != streamErrorLog {
		t.Fatalf("anthropic stream error log = %p, want %p", got, streamErrorLog)
	}

	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	tokenPath := filepath.Join(configHome, "steiner", "codex_auth.json")
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		t.Fatalf("mkdir token dir: %v", err)
	}
	if err := os.WriteFile(tokenPath, []byte(`{"access_token":"codex-token","refresh_token":"refresh-token","token_type":"Bearer","openai_api_key":"sk-codex"}`), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	codexRM := provider.ResolvedModel{
		Alias: "codex",
		ProviderConfig: config.ProviderConfig{
			Type:    config.ProviderTypeCodex,
			BaseURL: "https://api.openai.com/v1",
			Timeout: config.MustDuration("20s"),
			Codex:   config.CodexConfig{MinRequestInterval: config.MustDuration("4s")},
		},
		BackendModelID:        "gpt-5.5",
		EffectiveProviderType: config.ProviderTypeCodex,
	}
	gotCodex := runFactory(t, codexRM, "", "codex")
	if gotCodex.openAICompatCalls != 0 {
		t.Fatalf("codex should not call openai compat constructor, got %d calls", gotCodex.openAICompatCalls)
	}
	if got := gotCodex.codexCfg.BaseURL; got != codexRM.ProviderConfig.BaseURL {
		t.Fatalf("codex base URL = %q, want %q", got, codexRM.ProviderConfig.BaseURL)
	}
	if got := gotCodex.codexCfg.Model; got != codexRM.BackendModelID {
		t.Fatalf("codex model = %q, want %q", got, codexRM.BackendModelID)
	}
	if got := gotCodex.codexCfg.ProviderType; got != string(config.ProviderTypeCodex) {
		t.Fatalf("codex provider type = %q, want %q", got, config.ProviderTypeCodex)
	}
	if got := gotCodex.codexCfg.APIKey; got != "sk-codex" {
		t.Fatalf("codex API key = %q, want sk-codex", got)
	}
	if gotCodex.codexCfg.HTTPClient != httpClient {
		t.Fatalf("codex HTTP client = %p, want base runtime client %p", gotCodex.codexCfg.HTTPClient, httpClient)
	}
	// Codex pacing is the only per-request interval in the config, and it is the
	// field that would start pacing every provider if it were ever hoisted out of
	// NewCodexResponses into the shared client constructor.
	if got, want := gotCodex.codexCfg.MinRequestInterval, 4*time.Second; got != want {
		t.Fatalf("codex min request interval = %v, want %v", got, want)
	}
	if got := gotCodex.codexCfg.StreamErrorLog; got != streamErrorLog {
		t.Fatalf("codex stream error log = %p, want %p", got, streamErrorLog)
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

func TestBuildRuntimeProviderFactoryCodexUsesChatGPTBackendWithoutExchangedAPIKey(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	tokenPath := filepath.Join(configHome, "steiner", "codex_auth.json")
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		t.Fatalf("mkdir token dir: %v", err)
	}
	if err := os.WriteFile(tokenPath, []byte(`{"access_token":"codex-token","refresh_token":"refresh-token","token_type":"Bearer","account_id":"acct-123"}`), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	var gotCfg provider.ClientConfig
	oldNewCodexResponses := newCodexResponses
	t.Cleanup(func() { newCodexResponses = oldNewCodexResponses })
	newCodexResponses = func(cfg provider.ClientConfig) (provider.Provider, error) {
		gotCfg = cfg
		return &fakeProvider{}, nil
	}

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
	if _, err := factory(codexRM); err != nil {
		t.Fatalf("factory() error = %v", err)
	}
	if gotCfg.BaseURL != "https://chatgpt.com/backend-api/codex" {
		t.Fatalf("codex base URL = %q, want ChatGPT Codex backend", gotCfg.BaseURL)
	}
	if gotCfg.APIKey != "codex-token" {
		t.Fatalf("codex API key = %q, want access token", gotCfg.APIKey)
	}
	if got := gotCfg.Headers["ChatGPT-Account-ID"]; got != "acct-123" {
		t.Fatalf("ChatGPT-Account-ID = %q, want acct-123", got)
	}
}

func TestBuildRuntimeProviderFactoryCodexMissingAccountMetadata(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	tokenPath := filepath.Join(configHome, "steiner", "codex_auth.json")
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		t.Fatalf("mkdir token dir: %v", err)
	}
	if err := os.WriteFile(tokenPath, []byte(`{"access_token":"codex-token","refresh_token":"refresh-token","token_type":"Bearer"}`), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

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
	want := "codex token missing ChatGPT account metadata — run 'steiner login codex' again"
	if err.Error() != want {
		t.Fatalf("factory() error = %q, want %q", err.Error(), want)
	}
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

func TestBuildRuntimeSandbox_Bypassed(t *testing.T) {
	cfg := config.Config{
		Sandbox: config.SandboxConfig{Enabled: false},
	}

	sb, err := buildRuntimeSandbox(&cfg, "/tmp", "/tmp", "/tmp")
	if err != nil {
		t.Fatalf("expected nil error when sandbox is disabled, got: %v", err)
	}
	if sb != nil {
		t.Fatal("expected nil sandbox when disabled")
	}
}

func TestBuildRuntimeSandbox_Unavailable(t *testing.T) {
	cfg := config.Config{
		Sandbox: config.SandboxConfig{Enabled: true},
	}

	sb, err := buildRuntimeSandbox(&cfg, "/tmp", "/tmp", "/tmp")
	// On Linux with bwrap, this may succeed — skip if sandbox built.
	if sb != nil {
		t.Skip("bwrap is available, cannot test unavailable branch")
	}
	// Otherwise we expect nil error (graceful fallback).
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}
