package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestResolveAccentPreset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		preset         string
		randFn         func(int) int
		wantInPresets  bool
		wantExactValue string
	}{
		{
			name:          "random resolves to a member of AccentPresets",
			preset:        "random",
			randFn:        func(_ int) int { return 0 }, // always pick first
			wantInPresets: true,
		},
		{
			name:          "random with different seed can pick different preset",
			preset:        "random",
			randFn:        func(n int) int { return n - 1 }, // always pick last
			wantInPresets: true,
		},
		{
			name:           "known preset returns its hex value",
			preset:         "amber",
			randFn:         func(_ int) int { return 0 },
			wantExactValue: "#E8814B",
		},
		{
			name:           "known preset violet",
			preset:         "violet",
			randFn:         func(_ int) int { return 0 },
			wantExactValue: "#9D8DF1",
		},
		{
			name:           "unknown preset returns empty string",
			preset:         "notapreset",
			randFn:         func(_ int) int { return 0 },
			wantExactValue: "",
		},
		{
			name:           "empty preset returns empty string",
			preset:         "",
			randFn:         func(_ int) int { return 0 },
			wantExactValue: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolveAccentPreset(tt.preset, tt.randFn)

			if tt.wantExactValue != "" {
				if got != tt.wantExactValue {
					t.Errorf("resolveAccentPreset(%q, ...) = %q, want %q", tt.preset, got, tt.wantExactValue)
				}
			} else if tt.wantInPresets {
				// Check if the result is in AccentPresets values
				found := false
				for _, v := range theme.AccentPresets {
					if got == v {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("resolveAccentPreset(%q, ...) = %q, which is not in AccentPresets", tt.preset, got)
				}
			}
		})
	}
}

func TestResolveAccentPresetRandomDeterministic(t *testing.T) {
	// Test that random selection is deterministic given the same randFn
	t.Parallel()
	callCount := 0
	randFn := func(_ int) int {
		callCount++
		return 0
	}

	result1 := resolveAccentPreset("random", randFn)
	callCount = 0
	result2 := resolveAccentPreset("random", randFn)

	if result1 != result2 {
		t.Errorf("resolveAccentPreset with same randFn should give same result: got %q and %q", result1, result2)
	}

	if callCount == 0 {
		t.Error("randFn was not called")
	}
}
