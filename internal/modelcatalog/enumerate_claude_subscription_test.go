package modelcatalog

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestClaudeSubscriptionEnumeratorDiscovery(t *testing.T) {
	ep := Endpoint{Alias: "claude", Type: "claude_subscription", BaseURL: "claude-subscription://local"}
	want := []DiscoveredModel{{
		ProviderAlias: "claude", ProviderType: "claude_subscription", ID: "claude-live",
		DisplayName: "Claude Live", Description: "live model", SupportedEfforts: []string{"low", "high"},
	}}
	tests := []struct {
		name     string
		discover ClaudeSubscriptionDiscover
		want     []DiscoveredModel
	}{
		{
			name: "success",
			discover: func(context.Context) ([]DiscoveredModel, error) {
				return []DiscoveredModel{{ID: "claude-live", DisplayName: "Claude Live", Description: "live model", SupportedEfforts: []string{"low", "high"}}}, nil
			},
			want: want,
		},
		{
			name: "error fallback",
			discover: func(context.Context) ([]DiscoveredModel, error) {
				return nil, errors.New("discovery unavailable")
			},
			want: staticClaudeSubscriptionModels(ep),
		},
		{
			name: "models with error fallback",
			discover: func(context.Context) ([]DiscoveredModel, error) {
				return []DiscoveredModel{{ID: "ignored"}}, errors.New("partial discovery")
			},
			want: staticClaudeSubscriptionModels(ep),
		},
		{
			name: "empty fallback",
			discover: func(context.Context) ([]DiscoveredModel, error) {
				return []DiscoveredModel{}, nil
			},
			want: staticClaudeSubscriptionModels(ep),
		},
		{
			name: "nil fallback",
			want: staticClaudeSubscriptionModels(ep),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := NewClaudeSubscriptionEnumerator(tt.discover).Enumerate(context.Background(), ep, EnumerationOptions{ETag: "old"})
			if err != nil {
				t.Fatalf("Enumerate() error = %v, want nil", err)
			}
			if result.ETag != "" || result.NotModified {
				t.Fatalf("result cache fields = etag %q not_modified %v, want empty and false", result.ETag, result.NotModified)
			}
			if !reflect.DeepEqual(result.Models, tt.want) {
				t.Fatalf("models = %#v, want %#v", result.Models, tt.want)
			}
		})
	}
}

func staticClaudeSubscriptionModels(ep Endpoint) []DiscoveredModel {
	efforts := []string{"low", "medium", "high", "xhigh", "max"}
	return []DiscoveredModel{
		{ProviderAlias: ep.Alias, ProviderType: ep.Type, ID: "claude-opus-5-5", DisplayName: "Opus 5.5", SupportedEfforts: efforts},
		{ProviderAlias: ep.Alias, ProviderType: ep.Type, ID: "claude-sonnet-5-5", DisplayName: "Sonnet 5.5", SupportedEfforts: efforts},
		{ProviderAlias: ep.Alias, ProviderType: ep.Type, ID: "claude-haiku-5-5", DisplayName: "Haiku 5.5", SupportedEfforts: efforts},
		{ProviderAlias: ep.Alias, ProviderType: ep.Type, ID: "claude-fable-5-1", DisplayName: "Fable 5.1", SupportedEfforts: efforts},
	}
}
