package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/usagestats"
)

// tokenStatsSampleTable returns the over-wide session table from the overlay
// width report: a long provider and model with every column populated. Its
// natural width is 112: 80 cells, 22 padding and 10 separators.
func tokenStatsSampleTable() tokenStatsTable {
	return tokenStatsTable{
		columns: windowTableColumns,
		rows: []tokenStatsRow{{
			cells: []string{
				"opencode-go", "deepseek-v4.1-flash",
				"12.1k", "23.2k", "-", "300", "124", "35.5k",
				"65.7k", "65.7%", "<$0.01",
			},
		}},
	}
}

// tokenStatsHeaderLine returns the markdown header line of a report.
func tokenStatsHeaderLine(t *testing.T, report string) string {
	t.Helper()
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(line, "| Provider |") {
			return line
		}
	}
	t.Fatalf("report has no window table header: %q", report)
	return ""
}

func TestTokenStatsColumnSetsDropOrder(t *testing.T) {
	t.Parallel()
	all := "| Provider | Model | Reqs | Uncached | Read | Write | Output | Reason | Total | Hit | Cost |"
	withoutReason := "| Provider | Model | Reqs | Uncached | Read | Write | Output | Total | Hit | Cost |"
	withoutWrite := "| Provider | Model | Reqs | Uncached | Read | Output | Total | Hit | Cost |"
	minimum := "| Provider | Model | Reqs | Uncached | Read | Output | Total | Cost |"

	tests := []struct {
		name      string
		width     int
		want      string
		wantModel int
	}{
		{name: "room for every column", width: 140, want: all},
		{name: "exactly the full set", width: 120, want: all},
		{name: "one column short of the full set", width: 119, want: withoutReason},
		{name: "exactly without Reason", width: 111, want: withoutReason},
		{name: "one column short without Reason", width: 110, want: withoutWrite},
		{name: "exactly without Write", width: 103, want: withoutWrite},
		{name: "one column short without Write", width: 102, want: minimum},
		{name: "exactly the minimum set", width: 95, want: minimum},
		{name: "model shortened by one", width: 94, want: minimum, wantModel: 18},
		{name: "model shortened to the header width", width: 81, want: minimum, wantModel: 5},
		{name: "model shortened below the header", width: 80, want: "| Provider | Mod… | Reqs | Uncached | Read | Output | Total | Cost |", wantModel: 4},
		{name: "minimum set still overflows", width: 60, want: "| Provider | … | Reqs | Uncached | Read | Output | Total | Cost |", wantModel: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			table := tokenStatsSampleTable()
			plan := planTokenStatsTables([]tokenStatsTable{table}, tokenStatsTableLimit(tt.width))

			var sb strings.Builder
			writeTokenStatsTable(&sb, table, plan)
			if got := tokenStatsHeaderLine(t, sb.String()); got != tt.want {
				t.Fatalf("width %d header = %q, want %q", tt.width, got, tt.want)
			}
			if plan.modelWidth != tt.wantModel {
				t.Fatalf("width %d modelWidth = %d, want %d", tt.width, plan.modelWidth, tt.wantModel)
			}
			// A dropped column never changes the cells of the columns kept.
			if tt.wantModel == 0 {
				if !strings.Contains(sb.String(), "| opencode-go | deepseek-v4.1-flash |") {
					t.Fatalf("width %d table = %q, want the model name whole", tt.width, sb.String())
				}
				if !strings.Contains(sb.String(), "| <$0.01 |") || !strings.Contains(sb.String(), "| 65.7k |") {
					t.Fatalf("width %d table = %q, want Cost and Total intact", tt.width, sb.String())
				}
			}
			if width, _ := tokenStatsTableFit(table, plan); width > tokenStatsTableLimit(tt.width) && tt.wantModel > 0 && tt.width > 76 {
				t.Fatalf("width %d table width = %d, want at most the limit %d", tt.width, width, tokenStatsTableLimit(tt.width))
			}
		})
	}
}

// recordTokenStatsWideFixture records one priced request from a long-named
// model, the row shape that overflowed the overlay before the columns made
// room for it.
func recordTokenStatsWideFixture(t *testing.T, rec *usagestats.Recorder, now time.Time) {
	t.Helper()
	cost := 0.005
	rec.Record(usagestats.Observation{
		ProviderAlias: "opencode-go", ProviderType: "openai", BackendModelID: "deepseek-v4.1-flash",
		PromptTokens: 23200, CompletionTokens: 124, ReasoningTokens: 35,
		CacheReadTokens: 12100, CacheCreateTokens: 300, ReportedCostUSD: &cost,
		At: now.Add(-time.Hour),
	})
}

// tokenStatsReportLines renders a report through the same glamour path the
// overlay uses and returns its lines and the report width.
func tokenStatsReportLines(t *testing.T, report string, width int) ([]string, int) {
	t.Helper()
	styles := newModel(Config{}, nil)
	rendered, err := renderMarkdownBlock(markdownBlockParams{
		block:       report,
		width:       width,
		styles:      styles.styles,
		styleSheet:  styles.content.glamourStyleSheet,
		renderer:    nil,
		renderWidth: nil,
	})
	if err != nil {
		t.Fatalf("render report: %v", err)
	}
	return strings.Split(rendered, "\n"), width
}

func TestTokenStatsTablesFitEveryReportWidth(t *testing.T) {
	tests := []struct {
		width     int
		want      string
		wantModel string
	}{
		{width: 140, want: "Provider Model Reqs Uncached Read Write Output Reason Total Hit Cost", wantModel: "deepseek-v4.1-flash"},
		{width: 120, want: "Provider Model Reqs Uncached Read Write Output Reason Total Hit Cost", wantModel: "deepseek-v4.1-flash"},
		{width: 110, want: "Provider Model Reqs Uncached Read Output Total Hit Cost", wantModel: "deepseek-v4.1-flash"},
		{width: 100, want: "Provider Model Reqs Uncached Read Output Total Cost", wantModel: "deepseek-v4.1-flash"},
		{width: 90, want: "Provider Model Reqs Uncached Read Output Total Cost", wantModel: "deepseek-v4.1…"},
		{width: 80, want: "Provider Mod… Reqs Uncached Read Output Total Cost", wantModel: "dee…"},
	}

	for _, tt := range tests {
		t.Run(strings.TrimSpace(tt.wantModel), func(t *testing.T) {
			rec := newTokenStatsRecorder(t, tokenStatsNow)
			recordTokenStatsWideFixture(t, rec, tokenStatsNow)

			report := formatTokenStatsReport(rec, tokenStatsNow, tt.width)
			header := tokenStatsHeaderLine(t, report)
			got := strings.Trim(strings.ReplaceAll(strings.Trim(header, "|"), " | ", " "), " |")
			if got != tt.want {
				t.Fatalf("width %d columns = %q, want %q", tt.width, got, tt.want)
			}

			lines, width := tokenStatsReportLines(t, report, tt.width)
			var row string
			for _, line := range lines {
				if lipgloss.Width(line) > width {
					t.Fatalf("width %d rendered line %q is %d cells wide", tt.width, line, lipgloss.Width(line))
				}
				if strings.Contains(line, "opencode-go") {
					row = line
				}
			}
			if row == "" {
				t.Fatalf("width %d render has no model row: %q", tt.width, strings.Join(lines, "\n"))
			}
			if !strings.Contains(row, tt.wantModel) {
				t.Fatalf("width %d row = %q, want model %q", tt.width, row, tt.wantModel)
			}
			if !strings.Contains(row, "<$0.01") {
				t.Fatalf("width %d row = %q, want the Cost cell intact", tt.width, row)
			}
		})
	}
}

func TestTokenStatsTableOverflowFloor(t *testing.T) {
	t.Parallel()
	table := tokenStatsSampleTable()
	plan := planTokenStatsTables([]tokenStatsTable{table}, tokenStatsTableLimit(60))

	if plan.columns.reason || plan.columns.write || plan.columns.hit {
		t.Fatalf("columns at width 60 = %+v, want only the required columns", plan.columns)
	}
	if plan.modelWidth != 1 {
		t.Fatalf("modelWidth at width 60 = %d, want the one-cell floor", plan.modelWidth)
	}
	// The remaining columns alone are wider than the limit, so the table still
	// overflows and glamour shrinks it. Cost stays in the column list.
	if width, fixedWidth := tokenStatsTableFit(table, plan); fixedWidth <= tokenStatsTableLimit(60) {
		t.Fatalf("table width = %d with fixed part %d, want the fixed part to overflow the limit", width, fixedWidth)
	}
}

func TestTokenStatsOverlayResizeRefitsTables(t *testing.T) {
	now := time.Now()
	rec := newTokenStatsRecorder(t, now)
	recordTokenStatsWideFixture(t, rec, now)

	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 140, Height: 120})
	m.recorder = rec
	m.executeOpenTokenStatsAction()

	if reportWidth := m.contextOverlay.reportWidth(); reportWidth != 116 {
		t.Fatalf("report width = %d, want the 120-column overlay inner width", reportWidth)
	}
	wide := m.contextOverlay.content
	if !strings.Contains(wide, "| Provider | Model | Reqs | Uncached | Read | Write | Output | Total | Hit | Cost |") {
		t.Fatalf("wide report = %q, want Reason dropped at the widest overlay", wide)
	}

	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 120})
	narrow := m.contextOverlay.content
	if strings.Contains(narrow, "| Reason |") || strings.Contains(narrow, "| Write |") || strings.Contains(narrow, "| Hit |") {
		t.Fatalf("narrow report = %q, want only the required columns", narrow)
	}
	if !strings.Contains(narrow, "deepseek-v4.1-f…") {
		t.Fatalf("narrow report = %q, want the Model cell shortened to the pane", narrow)
	}
	for _, line := range m.contextOverlay.renderedLines {
		if lipgloss.Width(line) > m.contextOverlay.reportWidth() {
			t.Fatalf("rendered line %q is wider than the report width %d", line, m.contextOverlay.reportWidth())
		}
	}

	m = updateModel(t, m, tea.WindowSizeMsg{Width: 140, Height: 120})
	if got := m.contextOverlay.content; got != wide {
		t.Fatalf("report after growing back = %q, want the wide report %q", got, wide)
	}
}
