package provider

import "testing"

func TestModelEffortLabel(t *testing.T) {
	tests := []struct{ ref, effort, want string }{
		{"m", "", "m"},
		{"m", " high ", "m/high"},
		{" m ", "high", "m/high"},
	}
	for _, tt := range tests {
		if got := ModelEffortLabel(tt.ref, tt.effort); got != tt.want {
			t.Errorf("ModelEffortLabel(%q,%q) = %q, want %q", tt.ref, tt.effort, got, tt.want)
		}
	}
}

func TestResolvedModelDisplayRef(t *testing.T) {
	tests := []struct {
		name string
		m    ResolvedModel
		want string
	}{
		{"configured alias", ResolvedModel{Alias: "luna", ProviderAlias: "codex", ReasoningEffectiveEffort: "high"}, "codex/luna/high"},
		{"raw ref", ResolvedModel{Alias: "codex/gpt-5.6-luna", ProviderAlias: "codex"}, "codex/gpt-5.6-luna"},
		{"no provider", ResolvedModel{Alias: "luna", ReasoningEffectiveEffort: "low"}, "luna/low"},
		{"trimmed", ResolvedModel{Alias: " luna ", ProviderAlias: " codex ", ReasoningEffectiveEffort: " high "}, "codex/luna/high"},
		{"no alias", ResolvedModel{ProviderAlias: "codex"}, "codex"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.DisplayRef(); got != tt.want {
				t.Errorf("DisplayRef() = %q, want %q", got, tt.want)
			}
		})
	}
}
