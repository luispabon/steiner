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

func TestModelPromptsSystemSuffixPatchApply(t *testing.T) {
	cases := []struct {
		name  string
		base  ModelPrompts
		patch modelPromptsPatch
		want  string
	}{
		{
			name:  "sets system suffix",
			base:  ModelPrompts{},
			patch: modelPromptsPatch{SystemSuffix: strPtr("Extended thinking enabled")},
			want:  "Extended thinking enabled",
		},
		{
			name:  "empty string clears inherited system suffix",
			base:  ModelPrompts{SystemSuffix: "Extended thinking enabled"},
			patch: modelPromptsPatch{SystemSuffix: strPtr("")},
			want:  "",
		},
		{
			name:  "nil patch leaves base unchanged",
			base:  ModelPrompts{SystemSuffix: "Extended thinking enabled"},
			patch: modelPromptsPatch{},
			want:  "Extended thinking enabled",
		},
		{
			name:  "system suffix updates independently from system prompt",
			base:  ModelPrompts{System: "Be helpful", SystemSuffix: "Old suffix"},
			patch: modelPromptsPatch{SystemSuffix: strPtr("New suffix")},
			want:  "New suffix",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dst := tc.base
			applyModelPromptsPatch(&dst, &tc.patch)
			if dst.SystemSuffix != tc.want {
				t.Fatalf("SystemSuffix = %q, want %q", dst.SystemSuffix, tc.want)
			}
		})
	}
}
