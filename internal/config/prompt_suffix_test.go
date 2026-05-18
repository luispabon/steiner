package config

import "testing"

func strPtr(s string) *string { return &s }

func TestPromptSuffixPatchApply(t *testing.T) {
	cases := []struct {
		name  string
		base  ModelConfig
		patch modelPatch
		want  string
	}{
		{
			name:  "sets prompt suffix",
			base:  ModelConfig{},
			patch: modelPatch{PromptSuffix: strPtr("<|think_off|>")},
			want:  "<|think_off|>",
		},
		{
			name:  "empty string clears inherited suffix",
			base:  ModelConfig{PromptSuffix: "<|think_off|>"},
			patch: modelPatch{PromptSuffix: strPtr("")},
			want:  "",
		},
		{
			name:  "nil patch leaves base unchanged",
			base:  ModelConfig{PromptSuffix: "<|think_off|>"},
			patch: modelPatch{},
			want:  "<|think_off|>",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dst := tc.base
			applyModelPatch(&dst, &tc.patch)
			if dst.PromptSuffix != tc.want {
				t.Fatalf("PromptSuffix = %q, want %q", dst.PromptSuffix, tc.want)
			}
		})
	}
}
