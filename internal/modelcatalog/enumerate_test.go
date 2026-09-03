package modelcatalog

import (
	"net/http"
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
		{config.ProviderTypeAnthropic, false},
		{config.ProviderTypeCodex, false},
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
		{config.ProviderTypeAnthropic, true},
		{config.ProviderTypeCodex, true},
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
