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
		model        string
		wantProvider string
		wantNoSlash  bool
	}{
		{
			name:         "uses providerName when set",
			provider:     "https://api.anthropic.com",
			providerName: "anthropic",
			model:        "claude-sonnet-5",
			wantProvider: "anthropic/",
		},
		{
			name:         "falls back to stripProviderURL when providerName empty",
			provider:     "https://api.openai.com",
			providerName: "",
			model:        "gpt-5",
			wantProvider: "api.openai.com/",
		},
		{
			name:         "shows model alone with no leading slash when both empty",
			provider:     "",
			providerName: "",
			model:        "gpt-5",
			wantNoSlash:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := sidebarState{
				provider:     tc.provider,
				providerName: tc.providerName,
				model:        tc.model,
			}
			lines := s.modelSection(32)
			joined := strings.Join(lines, "\n")
			if !strings.Contains(joined, tc.model) {
				t.Errorf("modelSection() missing model %q in %q", tc.model, joined)
			}
			if tc.wantProvider != "" && !strings.Contains(joined, tc.wantProvider) {
				t.Errorf("modelSection() missing provider prefix %q in %q", tc.wantProvider, joined)
			}
			if tc.wantNoSlash && strings.Contains(joined, "/"+tc.model) {
				t.Errorf("modelSection() should not have a leading slash before model, got %q", joined)
			}
		})
	}
}

func TestModelSectionFittingKeepsModelIntact(t *testing.T) {
	t.Parallel()
	s := sidebarState{
		provider:     "https://a-very-long-provider-hostname.example.com",
		providerName: "",
		model:        "short-model",
	}
	lines := s.modelSection(20)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, s.model) {
		t.Errorf("modelSection() must never truncate the model name, got %q", joined)
	}
}

func TestModelSectionReasoningLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		reasoning string
		quant     string
		wantShown bool
		wantLine  string
	}{
		{name: "hidden when empty", reasoning: "", wantShown: false},
		{name: "shows effort", reasoning: "medium", wantShown: true, wantLine: "reasoning: medium"},
		{name: "shows provider default", reasoning: "provider default", wantShown: true, wantLine: "reasoning: provider default"},
		{name: "reasoning and quant combine on one line", reasoning: "high", quant: "q4_k_m", wantShown: true, wantLine: "reasoning: high · q4_k_m"},
		{name: "quant alone when reasoning empty", reasoning: "", quant: "q4_k_m", wantShown: true, wantLine: "quant: q4_k_m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := sidebarState{model: "gpt-5", reasoning: tc.reasoning, quant: tc.quant}
			lines := s.modelSection(32)
			joined := strings.Join(lines, "\n")
			if tc.wantShown && !strings.Contains(joined, tc.wantLine) {
				t.Errorf("modelSection() missing line %q in %q", tc.wantLine, joined)
			}
			if !tc.wantShown && strings.Contains(joined, "reasoning") {
				t.Errorf("modelSection() should not contain 'reasoning' label, got %q", joined)
			}
		})
	}
}

func TestStatusSection(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentAmber)
	cases := []struct {
		name          string
		sandboxStatus string
		activeSkill   string
		mcpTotal      int
		mcpConnected  int
		wantRows      []string
		wantNil       bool
	}{
		{name: "all absent", wantNil: true},
		{name: "sandbox only", sandboxStatus: "active", wantRows: []string{"SANDBOX"}},
		{name: "skill only", activeSkill: "review", wantRows: []string{"SKILL"}},
		{name: "mcp only", mcpTotal: 1, mcpConnected: 1, wantRows: []string{"MCP"}},
		{
			name:          "all present",
			sandboxStatus: "active",
			activeSkill:   "review",
			mcpTotal:      1,
			mcpConnected:  1,
			wantRows:      []string{"SANDBOX", "SKILL", "MCP"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := sidebarState{
				sandboxStatus: tc.sandboxStatus,
				activeSkill:   tc.activeSkill,
				mcpTotal:      tc.mcpTotal,
				mcpConnected:  tc.mcpConnected,
				styles:        styles,
			}
			got := s.statusSection(32)
			if tc.wantNil {
				if got != nil {
					t.Errorf("statusSection() = %v, want nil", got)
				}
				return
			}
			joined := strings.Join(got, "\n")
			for _, row := range tc.wantRows {
				if !strings.Contains(joined, row) {
					t.Errorf("statusSection() missing row %q in %q", row, joined)
				}
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
			wantLabel:             "CACHE HIT RATE",
			wantValue:             "—",
		},
		{
			name:                  "shows hit rate as percentage",
			sessionCacheHitRate:   0.782,
			sessionCacheHitRateOK: true,
			wantLabel:             "CACHE HIT RATE",
			wantValue:             "78.2%",
		},
		{
			name:                  "shows zero percent",
			sessionCacheHitRate:   0.0,
			sessionCacheHitRateOK: true,
			wantLabel:             "CACHE HIT RATE",
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

func TestContextGaugeLine(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentAmber)
	cases := []struct {
		name          string
		promptUsed    int
		contextBudget int
		wantContains  []string
	}{
		{
			name:          "shows bar, percentage, and compact counts",
			promptUsed:    92_000,
			contextBudget: 1_000_000,
			wantContains:  []string{"9%", "92k", "1.0m"},
		},
		{
			name:          "falls back to n/a when no budget set",
			promptUsed:    0,
			contextBudget: 0,
			wantContains:  []string{"n/a"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := sidebarState{promptUsed: tc.promptUsed, contextBudget: tc.contextBudget, styles: styles}
			got := s.contextGaugeLine(32)
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("contextGaugeLine() = %q, want to contain %q", got, want)
				}
			}
		})
	}
}

func TestContextSectionCompactDotOnlyWhenActive(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentAmber)

	idle := sidebarState{styles: styles}
	if joined := strings.Join(idle.contextSection(32), "\n"); strings.Contains(joined, "compacting") {
		t.Errorf("contextSection() should not show compacting dot when idle, got %q", joined)
	}

	active := sidebarState{styles: styles, compaction: compactionState{active: true}}
	if joined := strings.Join(active.contextSection(32), "\n"); !strings.Contains(joined, "compacting…") {
		t.Errorf("contextSection() should show compacting dot when active, got %q", joined)
	}
}

func TestStaticLinesLineCount(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentAmber)
	s := sidebarState{
		model:                 "gpt-5",
		reasoning:             "medium",
		providerName:          "openai",
		promptUsed:            92_000,
		contextBudget:         1_000_000,
		sandboxStatus:         "active",
		activeSkill:           "review",
		mcpTotal:              1,
		mcpConnected:          1,
		perfDurationMs:        1200,
		perfTTFTMs:            340,
		perfOutputTPS:         42.1,
		sessionCacheHitRate:   0.95,
		sessionCacheHitRateOK: true,
		branch:                "main",
		ahead:                 7,
		workingDir:            "/home/user/project",
		styles:                styles,
	}
	const want = 31
	if got := len(s.staticLines(32)); got != want {
		t.Errorf("len(staticLines(32)) = %d, want %d", got, want)
	}
}
