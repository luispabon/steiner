package main

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

func TestBuildRuntimeProviderFactoryDispatchesByResolvedProviderType(t *testing.T) {
	oldNewOpenAICompat := newOpenAICompat
	oldNewAnthropic := newAnthropic
	oldNewCodexResponses := newCodexResponses
	oldNewCodexResponsesWS := newCodexResponsesWS
	t.Cleanup(func() {
		newOpenAICompat = oldNewOpenAICompat
		newAnthropic = oldNewAnthropic
		newCodexResponses = oldNewCodexResponses
		newCodexResponsesWS = oldNewCodexResponsesWS
	})

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
		codexWSCalls      int
		openAICompatCfg   provider.ClientConfig
		anthropicCfg      provider.ClientConfig
		codexCfg          provider.ClientConfig
		codexWSCfg        provider.ClientConfig
	}

	runFactory := func(t *testing.T, rm provider.ResolvedModel, wantErr string, wantProviderKind string) capture {
		t.Helper()

		var got capture
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
		newCodexResponsesWS = func(cfg provider.ClientConfig) (provider.Provider, error) {
			got.codexWSCalls++
			got.codexWSCfg = cfg
			return &fakeProvider{}, nil
		}

		factory := buildRuntimeProviderFactory(config.Config{}, httpClient, streamErrorLog)

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
				t.Fatalf("codex HTTP constructor calls = %d, want 1 (default transport http)", got.codexCalls)
			}
			if got.codexWSCalls != 0 {
				t.Fatalf("codex WS constructor calls = %d, want 0", got.codexWSCalls)
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

	factory := buildRuntimeProviderFactory(config.Config{}, &http.Client{}, nil)

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

	factory := buildRuntimeProviderFactory(config.Config{}, &http.Client{}, nil)

	codexRM := provider.ResolvedModel{
		Alias:                 "codex",
		ProviderConfig:        config.ProviderConfig{Type: config.ProviderTypeCodex},
		BackendModelID:        "codex-default",
		EffectiveProviderType: config.ProviderTypeCodex,
	}
	_, err := factory(codexRM)
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

	factory := buildRuntimeProviderFactory(config.Config{}, &http.Client{}, nil)

	codexRM := provider.ResolvedModel{
		Alias:                 "codex",
		ProviderConfig:        config.ProviderConfig{Type: config.ProviderTypeCodex},
		BackendModelID:        "codex-default",
		EffectiveProviderType: config.ProviderTypeCodex,
	}
	_, err := factory(codexRM)
	if err == nil {
		t.Fatal("factory() error = nil, want actionable error")
	}
	want := "codex provider requires authentication — run 'steiner login codex' first"
	if err.Error() != want {
		t.Fatalf("factory() error = %q, want %q", err.Error(), want)
	}
}

func TestCodexTransportSwitch(t *testing.T) {
	tests := []struct {
		name      string
		transport config.CodexTransport
		wantWS    bool
		wantHTTP  bool
	}{
		{
			name:      "CodexTransportWebSocket uses the WebSocket transport",
			transport: config.CodexTransportWebSocket,
			wantWS:    true,
		},
		{
			name:      "CodexTransportHTTP uses HTTP only",
			transport: config.CodexTransportHTTP,
			wantHTTP:  true,
		},
		{
			name:      "empty transport defaults to HTTP",
			transport: "",
			wantHTTP:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configHome := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", configHome)
			tokenPath := filepath.Join(configHome, "steiner", "codex_auth.json")
			if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
				t.Fatalf("mkdir token dir: %v", err)
			}
			if err := os.WriteFile(tokenPath, []byte(`{"access_token":"api-key-123","refresh_token":"refresh","token_type":"Bearer","account_id":"acct-123"}`), 0o600); err != nil {
				t.Fatalf("write token: %v", err)
			}

			var wsCallCount, httpCallCount int
			oldWS := newCodexResponsesWS
			oldHTTP := newCodexResponses
			t.Cleanup(func() {
				newCodexResponsesWS = oldWS
				newCodexResponses = oldHTTP
			})

			newCodexResponsesWS = func(cfg provider.ClientConfig) (provider.Provider, error) {
				wsCallCount++
				return &fakeProvider{}, nil
			}
			newCodexResponses = func(cfg provider.ClientConfig) (provider.Provider, error) {
				httpCallCount++
				return &fakeProvider{}, nil
			}

			factory := buildRuntimeProviderFactory(config.Config{}, &http.Client{}, nil)

			codexRM := provider.ResolvedModel{
				Alias:                 "codex",
				ProviderConfig:        config.ProviderConfig{Type: config.ProviderTypeCodex, Codex: config.CodexConfig{Transport: tt.transport}},
				BackendModelID:        "codex-default",
				EffectiveProviderType: config.ProviderTypeCodex,
			}
			_, err := factory(codexRM)
			if err != nil {
				t.Fatalf("factory() error = %v", err)
			}

			if tt.wantWS {
				if wsCallCount != 1 {
					t.Fatalf("newCodexResponsesWS calls = %d, want 1", wsCallCount)
				}
				if httpCallCount != 0 {
					t.Fatalf("newCodexResponses calls = %d, want 0", httpCallCount)
				}
			}
			if tt.wantHTTP {
				if httpCallCount != 1 {
					t.Fatalf("newCodexResponses calls = %d, want 1", httpCallCount)
				}
				if wsCallCount != 0 {
					t.Fatalf("newCodexResponsesWS calls = %d, want 0", wsCallCount)
				}
			}
		})
	}
}

func TestBuildRuntimeSandbox_Bypassed(t *testing.T) {
	cfg := config.Config{
		Sandbox: config.SandboxConfig{Enabled: false},
	}

	sb, status, err := buildRuntimeSandbox(&cfg, "/tmp", "/tmp", "/tmp")
	if err != nil {
		t.Fatalf("expected nil error when sandbox is disabled, got: %v", err)
	}
	if sb != nil {
		t.Fatal("expected nil sandbox when disabled")
	}
	if status != "bypassed" {
		t.Fatalf("status = %q, want %q", status, "bypassed")
	}
}

func TestBuildRuntimeSandbox_Unavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	cfg := config.Config{
		Sandbox: config.SandboxConfig{Enabled: true},
	}

	sb, status, err := buildRuntimeSandbox(&cfg, "/tmp", "/tmp", "/tmp")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if sb != nil {
		t.Fatal("expected nil sandbox when bwrap is unavailable")
	}
	if status != "unavailable" {
		t.Fatalf("status = %q, want %q", status, "unavailable")
	}
}

func TestEmitSandboxWarning_EnvPassthroughAll(t *testing.T) {
	tests := []struct {
		name          string
		cfg           config.Config
		wantEmitted   bool
		wantSubstring string
	}{
		{
			name: "active sandbox with env_passthrough_all warns about the credential barrier",
			cfg: config.Config{
				Sandbox: config.SandboxConfig{Enabled: true, EnvPassthroughAll: true},
			},
			wantEmitted:   true,
			wantSubstring: "credential barrier is disabled",
		},
		{
			name: "active sandbox without env_passthrough_all emits nothing",
			cfg: config.Config{
				Sandbox: config.SandboxConfig{Enabled: true, EnvPassthroughAll: false},
			},
			wantEmitted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var emitted []output.Event
			sink := output.SinkFunc(func(e output.Event) { emitted = append(emitted, e) })

			emitSandboxWarning(tt.cfg, "active", sink)

			if !tt.wantEmitted {
				if len(emitted) != 0 {
					t.Fatalf("expected no events, got %d: %v", len(emitted), emitted)
				}
				return
			}
			if len(emitted) == 0 {
				t.Fatal("expected a SandboxStatusEvent, got none")
			}
			payload, ok := emitted[0].Payload.(output.SandboxStatusEvent)
			if !ok {
				t.Fatalf("payload type = %T, want output.SandboxStatusEvent", emitted[0].Payload)
			}
			if !strings.Contains(payload.Message, tt.wantSubstring) {
				t.Fatalf("message = %q, want substring %q", payload.Message, tt.wantSubstring)
			}
		})
	}
}

func TestEmitProjectContextDeprecationWarning(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.Config
		wantEmitted bool
	}{
		{
			name: "legacy max_tokens set emits deprecation warning",
			cfg: config.Config{
				ProjectContext: config.ProjectContextConfig{MaxTokens: 64},
			},
			wantEmitted: true,
		},
		{
			name:        "max_tokens unset emits nothing",
			cfg:         config.Config{},
			wantEmitted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var emitted []output.Event
			sink := output.SinkFunc(func(e output.Event) { emitted = append(emitted, e) })

			emitProjectContextDeprecationWarning(tt.cfg, sink)

			if !tt.wantEmitted {
				if len(emitted) != 0 {
					t.Fatalf("expected no events, got %d: %v", len(emitted), emitted)
				}
				return
			}
			if len(emitted) != 1 {
				t.Fatalf("expected 1 event, got %d: %v", len(emitted), emitted)
			}
			if got := emitted[0].Type; got != output.EventTypeConfigWarning {
				t.Fatalf("event type = %q, want %q", got, output.EventTypeConfigWarning)
			}
			if emitted[0].Type == output.EventTypeSandboxStatus {
				t.Fatal("event type = sandbox_status, want config_warning (must not overwrite sidebar sandbox status)")
			}
			payload, ok := emitted[0].Payload.(output.ConfigWarningEvent)
			if !ok {
				t.Fatalf("payload type = %T, want output.ConfigWarningEvent", emitted[0].Payload)
			}
			if !strings.Contains(payload.Message, "max_tokens") || !strings.Contains(payload.Message, "max_bytes") {
				t.Errorf("message = %q, want it to name max_tokens and max_bytes", payload.Message)
			}
		})
	}
}

func TestProjectContextConfigWarnings(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want []string
	}{
		{
			name: "max_tokens unset yields no warnings",
			cfg:  config.Config{},
			want: nil,
		},
		{
			name: "max_tokens set yields one warning",
			cfg:  config.Config{ProjectContext: config.ProjectContextConfig{MaxTokens: 64}},
			want: []string{"project_context.max_tokens is deprecated; use max_bytes (converted as max_tokens x 4)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := projectContextConfigWarnings(tt.cfg)
			if len(got) != len(tt.want) {
				t.Fatalf("warnings = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("warnings = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestSelectMCPStderr covers the rule that keeps MCP server subprocess output
// off the terminal: a log file wins when configured, io.Discard wins in
// interactive mode with no log file (this must never fall through to
// os.Stderr, or server output corrupts the live TUI), and os.Stderr is only
// used in non-interactive mode with no log file.
func TestSelectMCPStderr(t *testing.T) {
	sentinel := &bytes.Buffer{}

	tests := []struct {
		name     string
		logPath  string
		asyncMCP bool
		want     io.Writer
	}{
		{
			name:     "log file configured, interactive",
			logPath:  "/tmp/session-mcp.log",
			asyncMCP: true,
			want:     sentinel,
		},
		{
			name:     "log file configured, non-interactive",
			logPath:  "/tmp/session-mcp.log",
			asyncMCP: false,
			want:     sentinel,
		},
		{
			name:     "no log file, interactive: must be io.Discard, never os.Stderr",
			logPath:  "",
			asyncMCP: true,
			want:     io.Discard,
		},
		{
			name:     "no log file, non-interactive",
			logPath:  "",
			asyncMCP: false,
			want:     os.Stderr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectMCPStderr(tt.logPath, tt.asyncMCP, sentinel)
			if got == tt.want {
				return
			}
			if tt.logPath == "" && tt.asyncMCP {
				t.Fatalf("selectMCPStderr() = %v, want io.Discard — MCP server stderr must never reach the terminal in interactive mode with no log file configured", got)
			}
			t.Fatalf("selectMCPStderr() = %v, want %v", got, tt.want)
		})
	}
}

// TestSelectMCPStderr_LogPathFromConfig exercises the config-driven path (as
// opposed to the --log-file flag) that produces logPath, confirming the
// derived path threads through to selectMCPStderr's log-file branch.
func TestSelectMCPStderr_LogPathFromConfig(t *testing.T) {
	cfg := config.Config{Logging: config.LoggingConfig{Enabled: true, File: "/tmp/session.log"}}
	flags := &cliFlags{asyncMCP: true}

	logPath := mcp.ServerLogPath(runtimeLogFile(cfg, flags))
	if logPath == "" {
		t.Fatal("logPath = \"\", want non-empty when cfg.Logging.File is set")
	}

	sentinel := &bytes.Buffer{}
	got := selectMCPStderr(logPath, flags.asyncMCP, sentinel)
	if got != sentinel {
		t.Fatalf("selectMCPStderr() = %v, want the configured log writer when cfg.Logging.File drives logPath", got)
	}
}

func TestLoadRuntimeConfigProfileAndModelPrecedence(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, `providers:
  local:
    type: openai_compat
    base_url: http://example/v1
models:
  profiles:
    default:
      default_model: base
      sub_agents:
        code: base
    fast:
      default_model: fast
  definitions:
    base:
      provider: local
      id: base-model
    fast:
      provider: local
      id: fast-model
    cli:
      provider: local
      id: cli-model
    phase:
      provider: local
      id: phase-model
`)

	tests := []struct {
		name       string
		flags      cliFlags
		modelAlias string
		wantActive string
	}{
		{
			name: "named profile",
			flags: cliFlags{
				configPath: configPath,
				profile:    "fast",
			},
			wantActive: "fast",
		},
		{
			name: "cli model overrides profile",
			flags: cliFlags{
				configPath: configPath,
				model:      "cli",
				profile:    "fast",
			},
			wantActive: "cli",
		},
		{
			name: "phase model alias overrides cli model",
			flags: cliFlags{
				configPath: configPath,
				model:      "cli",
				profile:    "fast",
			},
			modelAlias: "phase",
			wantActive: "phase",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadRuntimeConfig(nil, &tt.flags, tt.modelAlias)
			if err != nil {
				t.Fatalf("loadRuntimeConfig() error = %v", err)
			}
			if got := cfg.Models.Effective.ProfileName; got != "fast" {
				t.Fatalf("effective profile = %q, want fast", got)
			}
			if got := cfg.Models.Effective.ActiveOrchestratorModel; got != tt.wantActive {
				t.Fatalf("active orchestrator model = %q, want %q", got, tt.wantActive)
			}
			if got := cfg.Models.Effective.DefaultModel; got != "fast" {
				t.Fatalf("effective default model = %q, want fast", got)
			}
			if got := cfg.Models.Effective.SubAgents["code"]; got != "base" {
				t.Fatalf("effective code model = %q, want inherited base", got)
			}
		})
	}
}

func TestLoadRuntimeConfigRejectsUnknownProfile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, `providers:
  local:
    type: openai_compat
    base_url: http://example/v1
models:
  profiles:
    default:
      default_model: base
  definitions:
    base:
      provider: local
      id: base-model
`)

	_, err := loadRuntimeConfig(nil, &cliFlags{configPath: configPath, profile: "missing"}, "")
	if err == nil || !strings.Contains(err.Error(), "profile is not defined") {
		t.Fatalf("loadRuntimeConfig() error = %v, want unknown profile error", err)
	}
}
