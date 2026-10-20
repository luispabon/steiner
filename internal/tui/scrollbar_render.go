package tui

import "strings"

func (m *Model) renderScrollbar() string {
	totalContent := m.viewport.TotalLineCount()
	if totalContent <= m.viewport.Height() {
		return ""
	}

	vh := m.viewport.Height()
	if vh <= 0 {
		return ""
	}

	// Check cache before recomputing.
	cacheKey := scrollbarCacheKey{yOffset: m.viewport.YOffset(), height: vh, totalLines: totalContent}
	if m.scrollbarCacheKey == cacheKey && m.scrollbarCacheRendered != "" {
		return m.scrollbarCacheRendered
	}

	thumbH := max(1, vh*vh/totalContent)
	trackH := vh - thumbH

	scrollRange := totalContent - vh
	var thumbPos int
	if scrollRange > 0 && trackH > 0 {
		thumbPos = int(float64(m.viewport.YOffset()) / float64(scrollRange) * float64(trackH))
	}
	if thumbPos > trackH {
		thumbPos = trackH
	}

	style := m.styles.Scrollbar
	trackStyle := m.styles.ScrollbarTrack
	if m.scrollbarCellStyles != m.styles {
		m.scrollbarCellStyles = m.styles
		m.scrollbarThumbCell = style.Render("▕")
		m.scrollbarTrackCell = trackStyle.Render(" ")
	}
	var sb strings.Builder
	sb.Grow(vh * (max(len(m.scrollbarThumbCell), len(m.scrollbarTrackCell)) + 1))
	sb.WriteString(strings.Repeat(m.scrollbarTrackCell+"\n", thumbPos))
	sb.WriteString(strings.Repeat(m.scrollbarThumbCell+"\n", thumbH))
	sb.WriteString(strings.Repeat(m.scrollbarTrackCell+"\n", vh-thumbPos-thumbH))
	result := strings.TrimSuffix(sb.String(), "\n")
	m.scrollbarCacheKey = cacheKey
	m.scrollbarCacheRendered = result
	return result
}
