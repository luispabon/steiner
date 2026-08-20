package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestFormatModelEffort(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		model  string
		effort string
		want   string
	}{
		{name: "without effort", model: "gpt-5", want: "gpt-5"},
		{name: "with effort", model: "gpt-5", effort: "high", want: "gpt-5/high"},
		{name: "trims model and effort", model: " gpt-5 ", effort: " high ", want: "gpt-5/high"},
		{name: "empty model", effort: "high", want: "/high"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatModelEffort(tc.model, tc.effort); got != tc.want {
				t.Errorf("formatModelEffort(%q, %q) = %q, want %q", tc.model, tc.effort, got, tc.want)
			}
		})
	}
}

func TestRenderModelBadge(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	cases := []struct {
		name   string
		model  string
		effort string
		want   string
	}{
		{name: "with effort", model: "gpt-5", effort: "high", want: "model gpt-5/high"},
		{name: "without effort", model: "gpt-5", want: "model gpt-5"},
		{name: "empty model", effort: "high", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := stripANSI(renderModelBadge(styles, tc.model, tc.effort))
			if tc.want == "" {
				if got != "" {
					t.Errorf("renderModelBadge(%q, %q) = %q, want empty", tc.model, tc.effort, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("renderModelBadge(%q, %q) = %q, want to contain %q", tc.model, tc.effort, got, tc.want)
			}
		})
	}
}
