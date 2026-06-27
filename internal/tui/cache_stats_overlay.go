package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/usagestats"
)

// formatCacheStatsReport renders a markdown cache-hit-rate report with one
// section per fixed window (last hour / 24h / 7d). Returns a "no data yet"
// message when the recorder is nil or every window is empty.
func formatCacheStatsReport(rec *usagestats.Recorder) string {
	if rec == nil {
		return "No cache statistics recorded yet."
	}

	var sb strings.Builder
	windows := []struct {
		label    string
		duration time.Duration
	}{
		{"Last hour", time.Hour},
		{"Last 24 hours", 24 * time.Hour},
		{"Last 7 days", 7 * 24 * time.Hour},
	}

	allEmpty := true
	for _, w := range windows {
		report := rec.Window(w.duration)
		if len(report.Rows) > 0 {
			allEmpty = false
		}

		fmt.Fprintf(&sb, "### %s\n\n", w.label)

		if len(report.Rows) == 0 {
			sb.WriteString("_No cache-capable calls in this window._\n")
		} else {
			// Sort rows deterministically
			slices.SortFunc(report.Rows, func(a, b usagestats.Row) int {
				if a.ProviderAlias != b.ProviderAlias {
					return strings.Compare(a.ProviderAlias, b.ProviderAlias)
				}
				return strings.Compare(a.BackendModelID, b.BackendModelID)
			})

			sb.WriteString("| Provider | Model | Hit rate | Cached / Total |\n")
			sb.WriteString("|----------|-------|----------|----------------|\n")

			for _, row := range report.Rows {
				hitRateStr := "—"
				if rate, ok := row.HitRate(); ok {
					hitRateStr = fmt.Sprintf("%.1f%%", rate*100)
				}

				cachedTotal := row.CacheReadTokens + row.InputTokens + row.CacheCreateTokens
				fmt.Fprintf(&sb, "| %s | %s | %s | %d / %d |\n",
					row.ProviderAlias,
					row.BackendModelID,
					hitRateStr,
					row.CacheReadTokens,
					cachedTotal,
				)
			}
		}

		sb.WriteString("\n")
	}

	if allEmpty {
		return "No cache statistics recorded yet."
	}

	return strings.TrimRight(sb.String(), "\n")
}

func (m Model) executeOpenCacheStatsAction() (tea.Model, tea.Cmd) {
	content := formatCacheStatsReport(m.recorder)
	m.contextOverlay = openContextOverlay("Cache hit rate", content, m.width, m.height, m.styles, m.content.glamourStyleSheet)
	m.input.Reset()
	m.historyIdx = 0
	m.relayoutInput()
	m.syncViewport()
	return m, nil
}
