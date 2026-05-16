package config

import (
	"testing"
)

func strPtr(s string) *string { return &s }

func TestThinkingFieldsPatchApply(t *testing.T) {
	cases := []struct {
		name  string
		base  ModelConfig
		patch modelPatch
		want  ModelConfig
	}{
		{
			name:  "disable thinking overrides enabled default",
			base:  ModelConfig{ThinkingEnabled: true},
			patch: modelPatch{ThinkingEnabled: boolPtr(false)},
			want:  ModelConfig{ThinkingEnabled: false},
		},
		{
			name:  "enable scaffold inference",
			base:  ModelConfig{ThinkingEnabled: true, ThinkingScaffoldInference: false},
			patch: modelPatch{ThinkingScaffoldInference: boolPtr(true)},
			want:  ModelConfig{ThinkingEnabled: true, ThinkingScaffoldInference: true},
		},
		{
			name:  "set disable marker",
			base:  ModelConfig{ThinkingEnabled: true},
			patch: modelPatch{ThinkingDisableMarker: strPtr("<|think_off|>")},
			want:  ModelConfig{ThinkingEnabled: true, ThinkingDisableMarker: "<|think_off|>"},
		},
		{
			name:  "set thinking params",
			base:  ModelConfig{ThinkingEnabled: true},
			patch: modelPatch{ThinkingParams: &map[string]any{"thinking": map[string]any{"type": "enabled"}}},
			want:  ModelConfig{ThinkingEnabled: true, ThinkingParams: map[string]any{"thinking": map[string]any{"type": "enabled"}}},
		},
		{
			name:  "nil patch leaves base unchanged",
			base:  ModelConfig{ThinkingEnabled: true, ThinkingScaffoldInference: false},
			patch: modelPatch{},
			want:  ModelConfig{ThinkingEnabled: true, ThinkingScaffoldInference: false},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dst := tc.base
			applyModelPatch(&dst, &tc.patch)
			if dst.ThinkingEnabled != tc.want.ThinkingEnabled {
				t.Errorf("ThinkingEnabled: got %v, want %v", dst.ThinkingEnabled, tc.want.ThinkingEnabled)
			}
			if dst.ThinkingScaffoldInference != tc.want.ThinkingScaffoldInference {
				t.Errorf("ThinkingScaffoldInference: got %v, want %v", dst.ThinkingScaffoldInference, tc.want.ThinkingScaffoldInference)
			}
			if dst.ThinkingDisableMarker != tc.want.ThinkingDisableMarker {
				t.Errorf("ThinkingDisableMarker: got %q, want %q", dst.ThinkingDisableMarker, tc.want.ThinkingDisableMarker)
			}
		})
	}
}
