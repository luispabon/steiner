package modelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestForTypeWithClient(t *testing.T) {
	tests := []struct {
		providerType config.ProviderType
		wantErr      bool
	}{
		{config.ProviderTypeOpenAI, false},
		{config.ProviderTypeOpenAICompat, false},
		{config.ProviderTypeLiteLLM, false},
		{config.ProviderTypeOpencodeGo, false},
		{config.ProviderTypeOpencodeZen, false},
		{config.ProviderTypeOllama, false},
		{config.ProviderTypeLMStudio, false},
		{config.ProviderTypeOpenRouter, false},
		{config.ProviderTypeMistral, false},
		{config.ProviderTypeAnthropic, false},
		{config.ProviderTypeCodex, false},
		{config.ProviderTypeClaudeSubscription, false},
		{config.ProviderType("unknown"), true},
	}

	for _, tt := range tests {
		t.Run(string(tt.providerType), func(t *testing.T) {
			e, err := ForTypeWithClient(tt.providerType, http.DefaultClient)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ForTypeWithClient() error = nil, want error")
				}
				if e != nil {
					t.Errorf("ForTypeWithClient() enumerator = %v, want nil on error", e)
				}
				return
			}
			if err != nil {
				t.Errorf("ForTypeWithClient() error = %v, want nil", err)
			}
			if e == nil {
				t.Errorf("ForTypeWithClient() enumerator = nil, want non-nil")
			}
		})
	}
}

func TestSupportsType(t *testing.T) {
	tests := []struct {
		providerType config.ProviderType
		want         bool
	}{
		{config.ProviderTypeOpenAI, true},
		{config.ProviderTypeOpenAICompat, true},
		{config.ProviderTypeLiteLLM, true},
		{config.ProviderTypeOpencodeGo, true},
		{config.ProviderTypeOpencodeZen, true},
		{config.ProviderTypeOllama, true},
		{config.ProviderTypeLMStudio, true},
		{config.ProviderTypeOpenRouter, true},
		{config.ProviderTypeMistral, true},
		{config.ProviderTypeAnthropic, true},
		{config.ProviderTypeCodex, true},
		{config.ProviderTypeClaudeSubscription, true},
		{config.ProviderType("unknown"), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.providerType), func(t *testing.T) {
			got := SupportsType(tt.providerType)
			if got != tt.want {
				t.Errorf("SupportsType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMistralEnumeratesOpenAIStyleModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path: got %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer mistral-key" {
			t.Errorf("authorization: got %q", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"mistral-large-latest"}]}`))
	}))
	defer server.Close()

	e, err := ForTypeWithClient(config.ProviderTypeMistral, server.Client())
	if err != nil {
		t.Fatalf("ForTypeWithClient() error = %v", err)
	}
	result, err := e.Enumerate(context.Background(), Endpoint{
		Alias: "mistral", Type: string(config.ProviderTypeMistral), BaseURL: server.URL, APIKey: "mistral-key",
	}, EnumerationOptions{})
	if err != nil {
		t.Fatalf("Enumerate() error = %v", err)
	}
	if len(result.Models) != 1 || result.Models[0].ID != "mistral-large-latest" {
		t.Fatalf("models = %+v, want mistral-large-latest", result.Models)
	}
}
