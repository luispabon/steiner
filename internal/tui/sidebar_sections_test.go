package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestFormatDuration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ms   int64
		want string
	}{
		{0, "—"},
		{-1, "—"},
		{500, "500ms"},
		{999, "999ms"},
		{1000, "1.0s"},
		{1200, "1.2s"},
		{5000, "5.0s"},
	}
	for _, tc := range cases {
		got := formatDuration(tc.ms)
		if got != tc.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tc.ms, got, tc.want)
		}
	}
}

func TestFormatTPS(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tps  float64
		want string
	}{
		{0, "—"},
		{-1, "—"},
		{42.1, "42.1 t/s"},
		{10.0, "10.0 t/s"},
	}
	for _, tc := range cases {
		got := formatTPS(tc.tps)
		if got != tc.want {
			t.Errorf("formatTPS(%f) = %q, want %q", tc.tps, got, tc.want)
		}
	}
}

func TestModelSectionProviderLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		provider     string
		providerName string
		wantLabel    string
		wantValue    string
	}{
		{
			name:         "uses providerName when set",
			provider:     "https://api.anthropic.com",
			providerName: "anthropic",
			wantLabel:    "provider",
			wantValue:    "anthropic",
		},
		{
			name:         "falls back to stripProviderURL when providerName empty",
			provider:     "https://api.openai.com",
			providerName: "",
			wantLabel:    "provider",
			wantValue:    "api.openai.com",
		},
		{
			name:         "shows n/a when both empty",
			provider:     "",
			providerName: "",
			wantLabel:    "provider",
			wantValue:    "n/a",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := sidebarState{
				provider:     tc.provider,
				providerName: tc.providerName,
			}
			lines := s.modelSection(32)
			joined := strings.Join(lines, "\n")
			if !strings.Contains(joined, tc.wantLabel) {
				t.Errorf("modelSection() missing label %q in %q", tc.wantLabel, joined)
			}
			if !strings.Contains(joined, tc.wantValue) {
				t.Errorf("modelSection() missing value %q in %q", tc.wantValue, joined)
			}
			if strings.Contains(joined, "host") {
				t.Errorf("modelSection() must not contain old label %q, got %q", "host", joined)
			}
		})
	}
}

func TestModelSectionReasoningLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		reasoning string
		wantShown bool
	}{
		{"hidden when empty", "", false},
		{"shows effort", "medium", true},
		{"shows provider default", "provider default", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := sidebarState{model: "gpt-5", reasoning: tc.reasoning}
			lines := s.modelSection(32)
			joined := strings.Join(lines, "\n")
			if tc.wantShown && !strings.Contains(joined, tc.reasoning) {
				t.Errorf("modelSection() missing reasoning value %q in %q", tc.reasoning, joined)
			}
			if !tc.wantShown && strings.Contains(joined, "reasoning") {
				t.Errorf("modelSection() should not contain 'reasoning' label, got %q", joined)
			}
		})
	}
}

func TestSeparatorLineMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		mode    string
		want    string
		hasMode bool
	}{
		{name: "plan", mode: "plan", want: " plan ", hasMode: true},
		{name: "build", mode: "build", want: " build ", hasMode: true},
		{name: "unset", mode: "", hasMode: false},
	}
	styles := theme.BuildStyles(theme.AccentAmber)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := sidebarState{execMode: tc.mode, styles: styles}
			line := s.separatorLine(32)
			if tc.hasMode {
				if !strings.Contains(line, tc.want) {
					t.Errorf("separatorLine() missing %q in %q", tc.want, line)
				}
				if !strings.Contains(line, "─") {
					t.Errorf("separatorLine() missing dashes in %q", line)
				}
			} else {
				if strings.Contains(line, " plan ") || strings.Contains(line, " build ") {
					t.Errorf("separatorLine() should not contain mode label when mode unset, got %q", line)
				}
				if !strings.Contains(line, "─") {
					t.Errorf("separatorLine() should contain dashes, got %q", line)
				}
			}
		})
	}
}

func TestPerformanceSection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		perfDurationMs int64
		perfTTFTMs     int64
		perfOutputTPS  float64
	}{
		{"all zeros", 0, 0, 0.0},
		{"with duration", 1200, 0, 0.0},
		{"with ttft", 0, 340, 0.0},
		{"with tps", 0, 0, 42.1},
		{"all values", 1200, 340, 42.1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := sidebarState{
				perfDurationMs: tc.perfDurationMs,
				perfTTFTMs:     tc.perfTTFTMs,
				perfOutputTPS:  tc.perfOutputTPS,
			}
			got := s.performanceSection(32)
			if len(got) == 0 {
				t.Errorf("performanceSection() = empty, always want non-empty")
			}
		})
	}
}

func TestSidebarRendersOneshotSection(t *testing.T) {
	t.Parallel()
	s := sidebarState{oneshotPhase: "plan"}
	lines := s.staticLines(32)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "ONESHOT") {
		t.Errorf("staticLines missing 'ONESHOT' in %q", joined)
	}
	if !strings.Contains(joined, "PLAN") {
		t.Errorf("staticLines missing 'PLAN' in %q", joined)
	}
}

func TestSidebarOmitsOneshotSectionWhenEmpty(t *testing.T) {
	t.Parallel()
	s := sidebarState{oneshotPhase: ""}
	lines := s.staticLines(32)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "ONESHOT") {
		t.Errorf("staticLines should not contain 'ONESHOT', got %q", joined)
	}
}

func TestFormatCacheHitRate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		rate float64
		ok   bool
		want string
	}{
		{0.0, false, "—"},
		{0.0, true, "0.0%"},
		{0.782, true, "78.2%"},
		{1.0, true, "100.0%"},
		{0.123, true, "12.3%"},
	}
	for _, tc := range cases {
		got := formatCacheHitRate(tc.rate, tc.ok)
		if got != tc.want {
			t.Errorf("formatCacheHitRate(%f, %v) = %q, want %q", tc.rate, tc.ok, got, tc.want)
		}
	}
}

func TestCacheSection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                  string
		sessionCacheHitRate   float64
		sessionCacheHitRateOK bool
		wantLabel             string
		wantValue             string
	}{
		{
			name:                  "shows undefined as dash",
			sessionCacheHitRate:   0.0,
			sessionCacheHitRateOK: false,
			wantLabel:             "CACHE",
			wantValue:             "—",
		},
		{
			name:                  "shows hit rate as percentage",
			sessionCacheHitRate:   0.782,
			sessionCacheHitRateOK: true,
			wantLabel:             "CACHE",
			wantValue:             "78.2%",
		},
		{
			name:                  "shows zero percent",
			sessionCacheHitRate:   0.0,
			sessionCacheHitRateOK: true,
			wantLabel:             "CACHE",
			wantValue:             "0.0%",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := sidebarState{
				sessionCacheHitRate:   tc.sessionCacheHitRate,
				sessionCacheHitRateOK: tc.sessionCacheHitRateOK,
			}
			got := s.cacheSection(32)
			if len(got) == 0 {
				t.Errorf("cacheSection() = empty, always want non-empty")
			}
			joined := strings.Join(got, "\n")
			if !strings.Contains(joined, tc.wantLabel) {
				t.Errorf("cacheSection() missing label %q in %q", tc.wantLabel, joined)
			}
			if !strings.Contains(joined, tc.wantValue) {
				t.Errorf("cacheSection() missing value %q in %q", tc.wantValue, joined)
			}
		})
	}
}
