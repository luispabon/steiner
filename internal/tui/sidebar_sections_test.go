package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

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
				styles:       testStyles(theme.AccentAmber),
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
		styles:       testStyles(theme.AccentAmber),
	}
	lines := s.modelSection(20)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, s.model) {
		t.Errorf("modelSection() must never truncate the model name, got %q", joined)
	}
}

func TestModelSectionReasoningAndQuant(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		reasoning    string
		quant        string
		wantModel    string
		wantQuant    string
		wantNoReason bool
	}{
		{name: "hidden when empty", wantModel: "gpt-5", wantNoReason: true},
		{name: "folds effort into model", reasoning: "medium", wantModel: "gpt-5/medium", wantNoReason: true},
		{name: "folds provider default into model", reasoning: "provider default", wantModel: "gpt-5/provider default", wantNoReason: true},
		{name: "keeps quant separate", reasoning: "high", quant: "q4_k_m", wantModel: "gpt-5/high", wantQuant: "quant: q4_k_m", wantNoReason: true},
		{name: "quant alone", quant: "q4_k_m", wantModel: "gpt-5", wantQuant: "quant: q4_k_m", wantNoReason: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := sidebarState{model: "gpt-5", reasoning: tc.reasoning, quant: tc.quant, styles: testStyles(theme.AccentAmber)}
			joined := strings.Join(s.modelSection(32), "\n")
			if !strings.Contains(joined, tc.wantModel) {
				t.Errorf("modelSection() missing model %q in %q", tc.wantModel, joined)
			}
			if tc.wantQuant != "" && !strings.Contains(joined, tc.wantQuant) {
				t.Errorf("modelSection() missing quant line %q in %q", tc.wantQuant, joined)
			}
			if tc.wantNoReason && strings.Contains(joined, "reasoning:") {
				t.Errorf("modelSection() should not contain reasoning label, got %q", joined)
			}
		})
	}
}

func TestStatusSection(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
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

func TestStatusSectionAccentKeys(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	s := sidebarState{
		sandboxStatus: "active",
		activeSkill:   "review",
		mcpTotal:      1,
		mcpConnected:  1,
		styles:        styles,
	}
	got := s.statusSection(32)
	if len(got) != 4 {
		t.Fatalf("statusSection() len = %d, want 4 (blank + 3 rows)", len(got))
	}
	bg := lipgloss.Color(theme.Black)
	wantSandboxKey := styles.CardLabel.Background(bg).Render("SANDBOX ")
	wantSkillKey := styles.CardLabel.Background(bg).Render("SKILL   ")
	wantMCPKey := styles.CardLabel.Background(bg).Render("MCP     ")
	rows := []struct {
		name string
		line string
		want string
	}{
		{"SANDBOX", got[1], wantSandboxKey},
		{"SKILL", got[2], wantSkillKey},
		{"MCP", got[3], wantMCPKey},
	}
	for _, tc := range rows {
		if !strings.HasPrefix(tc.line, tc.want) {
			t.Errorf("%s row = %q, want key prefix %q", tc.name, tc.line, tc.want)
		}
	}
}

func TestStatusSectionSandboxStatusBoundedToWidth(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	const width = 32
	s := sidebarState{
		sandboxStatus: strings.Repeat("x", 40),
		styles:        styles,
	}
	got := s.statusSection(width)
	for _, line := range got {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("statusSection() row width = %d, want <= %d, row = %q", w, width, line)
		}
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
	styles := testStyles(theme.AccentAmber)
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
		name                  string
		perfDurationMs        int64
		perfTTFTMs            int64
		perfOutputTPS         float64
		sessionCacheHitRate   float64
		sessionCacheHitRateOK bool
		wantCacheHitValue     string
	}{
		{"all zeros", 0, 0, 0.0, 0.95, true, "95.0%"},
		{"with duration", 1200, 0, 0.0, 0.95, true, "95.0%"},
		{"with ttft", 0, 340, 0.0, 0.95, true, "95.0%"},
		{"with tps", 0, 0, 42.1, 0.95, true, "95.0%"},
		{"all values", 1200, 340, 42.1, 0.95, true, "95.0%"},
		{"cache hit undefined", 0, 0, 0.0, 0.0, false, "—"},
		{"cache hit zero percent", 0, 0, 0.0, 0.0, true, "0.0%"},
		{"cache hit partial", 0, 0, 0.0, 0.782, true, "78.2%"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := sidebarState{
				perfDurationMs:        tc.perfDurationMs,
				perfTTFTMs:            tc.perfTTFTMs,
				perfOutputTPS:         tc.perfOutputTPS,
				sessionCacheHitRate:   tc.sessionCacheHitRate,
				sessionCacheHitRateOK: tc.sessionCacheHitRateOK,
				styles:                testStyles(theme.AccentAmber),
			}
			got := s.performanceSection(32)
			if len(got) == 0 {
				t.Errorf("performanceSection() = empty, always want non-empty")
			}
			joined := strings.Join(got, "\n")
			if !strings.Contains(joined, "PERFORMANCE") {
				t.Errorf("performanceSection() missing label %q in %q", "PERFORMANCE", joined)
			}
			if !strings.Contains(joined, tc.wantCacheHitValue) {
				t.Errorf("performanceSection() missing cache hit value %q in %q", tc.wantCacheHitValue, joined)
			}
		})
	}
}

func TestSidebarRendersOneshotSection(t *testing.T) {
	t.Parallel()
	s := sidebarState{oneshotPhase: "plan", styles: testStyles(theme.AccentAmber)}
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
	s := sidebarState{oneshotPhase: "", styles: testStyles(theme.AccentAmber)}
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

func TestContextGaugeLine(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	cases := []struct {
		name          string
		promptUsed    int
		contextBudget int
		wantContains  []string
		wantBarPrefix string
	}{
		{
			name:          "shows bar, percentage, and compact counts",
			promptUsed:    92_000,
			contextBudget: 1_000_000,
			wantContains:  []string{"9%", "92k", "1.0m"},
			wantBarPrefix: "▉",
		},
		{
			name:          "falls back to n/a when no budget set",
			promptUsed:    0,
			contextBudget: 0,
			wantContains:  []string{"n/a"},
		},
		{
			name:          "zero usage renders no glyphs",
			promptUsed:    0,
			contextBudget: 1_000_000,
			wantBarPrefix: "",
		},
		{
			name:          "tiny usage still renders a visible glyph",
			promptUsed:    1,
			contextBudget: 1_000_000,
			wantBarPrefix: "▏",
		},
		{
			name:          "partial eighth beyond a full cell",
			promptUsed:    125_000,
			contextBudget: 1_000_000,
			wantBarPrefix: "█▎",
		},
		{
			name:          "half full",
			promptUsed:    500_000,
			contextBudget: 1_000_000,
			wantBarPrefix: "█████",
		},
		{
			name:          "exactly full",
			promptUsed:    1_000_000,
			contextBudget: 1_000_000,
			wantBarPrefix: "██████████",
		},
		{
			name:          "over budget clamps to full",
			promptUsed:    2_000_000,
			contextBudget: 1_000_000,
			wantBarPrefix: "██████████",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := sidebarState{promptUsed: tc.promptUsed, contextBudget: tc.contextBudget, styles: styles}
			got := s.contextGaugeLine(32)
			if w := lipgloss.Width(got); w != 32 {
				t.Errorf("contextGaugeLine() width = %d, want 32, got %q", w, stripANSI(got))
			}
			plain := stripANSI(got)
			for _, want := range tc.wantContains {
				if !strings.Contains(plain, want) {
					t.Errorf("contextGaugeLine() = %q, want to contain %q", plain, want)
				}
			}
			if !strings.HasPrefix(plain, tc.wantBarPrefix) {
				t.Errorf("contextGaugeLine() = %q, want bar prefix %q", plain, tc.wantBarPrefix)
			}
			if tc.wantBarPrefix == "" && tc.promptUsed == 0 && tc.contextBudget > 0 {
				for _, glyph := range eighthBlocks {
					if strings.ContainsRune(plain, glyph) {
						t.Errorf("contextGaugeLine() = %q, want no bar glyphs at zero usage", plain)
					}
				}
			}
		})
	}
}

func TestContextSectionCompactDotOnlyWhenActive(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)

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
	styles := testStyles(theme.AccentAmber)
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
	const want = 28
	if got := len(s.staticLines(32)); got != want {
		t.Errorf("len(staticLines(32)) = %d, want %d", got, want)
	}
}
