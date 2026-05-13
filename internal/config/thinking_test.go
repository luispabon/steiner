package config

import (
	"testing"
)

func TestThinkingConfigPatchApply(t *testing.T) {
	cases := []struct {
		name  string
		base  ThinkingConfig
		patch thinkingConfigPatch
		want  ThinkingConfig
	}{
		{
			name:  "enable false overrides true default",
			base:  ThinkingConfig{Enabled: true},
			patch: thinkingConfigPatch{Enabled: boolPtr(false)},
			want:  ThinkingConfig{Enabled: false},
		},
		{
			name:  "enable scaffold inference",
			base:  ThinkingConfig{Enabled: true, EnabledScaffoldInference: false},
			patch: thinkingConfigPatch{EnabledScaffoldInference: boolPtr(true)},
			want:  ThinkingConfig{Enabled: true, EnabledScaffoldInference: true},
		},
		{
			name:  "set disable marker",
			base:  ThinkingConfig{Enabled: true},
			patch: thinkingConfigPatch{DisableMarker: strPtr("<|think_off|>")},
			want:  ThinkingConfig{Enabled: true, DisableMarker: "<|think_off|>"},
		},
		{
			name:  "set params",
			base:  ThinkingConfig{Enabled: true},
			patch: thinkingConfigPatch{Params: &map[string]any{"thinking": map[string]any{"type": "enabled"}}},
			want:  ThinkingConfig{Enabled: true, Params: map[string]any{"thinking": map[string]any{"type": "enabled"}}},
		},
		{
			name:  "nil patch leaves base unchanged",
			base:  ThinkingConfig{Enabled: true, EnabledScaffoldInference: false},
			patch: thinkingConfigPatch{},
			want:  ThinkingConfig{Enabled: true, EnabledScaffoldInference: false},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dst := ModelConfig{Thinking: tc.base}
			mp := modelPatch{Thinking: &tc.patch}
			applyModelPatch(&dst, &mp)
			got := dst.Thinking
			if got.Enabled != tc.want.Enabled {
				t.Errorf("Enabled: got %v, want %v", got.Enabled, tc.want.Enabled)
			}
			if got.EnabledScaffoldInference != tc.want.EnabledScaffoldInference {
				t.Errorf("EnabledScaffoldInference: got %v, want %v", got.EnabledScaffoldInference, tc.want.EnabledScaffoldInference)
			}
			if got.DisableMarker != tc.want.DisableMarker {
				t.Errorf("DisableMarker: got %q, want %q", got.DisableMarker, tc.want.DisableMarker)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
