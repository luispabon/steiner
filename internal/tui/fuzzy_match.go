package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"

	"github.com/luispabon/steiner/internal/tui/theme"
)

type slashOverlayMatch struct {
	commandIndexes []int
	nameIndexes    []int
	descIndexes    []int
}

type slashOverlaySource []slashOverlayItem

func (s slashOverlaySource) String(i int) string {
	item := s[i]
	return strings.Join([]string{item.command, item.name, item.desc}, " ")
}

func (s slashOverlaySource) Len() int { return len(s) }

func fuzzyMatchStrings(entries []string, query string) ([]string, [][]int) {
	q := strings.TrimSpace(query)
	if q == "" {
		return append([]string(nil), entries...), make([][]int, len(entries))
	}

	matches := fuzzy.Find(q, entries)
	results := make([]string, 0, len(matches))
	indexes := make([][]int, 0, len(matches))
	for _, match := range matches {
		results = append(results, entries[match.Index])
		indexes = append(indexes, append([]int(nil), match.MatchedIndexes...))
	}
	return results, indexes
}

func fuzzyMatchSlashItems(items []slashOverlayItem, query string) ([]slashOverlayItem, []slashOverlayMatch) {
	q := strings.TrimSpace(strings.TrimPrefix(query, "/"))
	if q == "" {
		return append([]slashOverlayItem(nil), items...), make([]slashOverlayMatch, len(items))
	}

	matches := fuzzy.FindFrom(q, slashOverlaySource(items))
	results := make([]slashOverlayItem, 0, len(matches))
	matchData := make([]slashOverlayMatch, 0, len(matches))
	for _, match := range matches {
		item := items[match.Index]
		results = append(results, item)
		matchData = append(matchData, splitSlashOverlayMatch(item, match.MatchedIndexes))
	}
	return results, matchData
}

func splitSlashOverlayMatch(item slashOverlayItem, indexes []int) slashOverlayMatch {
	commandLen := len(item.command)
	nameOffset := commandLen + 1
	nameLen := len(item.name)
	descOffset := nameOffset + nameLen + 1

	var result slashOverlayMatch
	for _, idx := range indexes {
		switch {
		case idx < commandLen:
			result.commandIndexes = append(result.commandIndexes, idx)
		case idx >= nameOffset && idx < nameOffset+nameLen:
			result.nameIndexes = append(result.nameIndexes, idx-nameOffset)
		case idx >= descOffset && idx < descOffset+len(item.desc):
			result.descIndexes = append(result.descIndexes, idx-descOffset)
		}
	}
	return result
}

func renderMatchedText(text string, matchedIndexes []int, baseStyle lipgloss.Style, matchedColor lipgloss.Color) string {
	if text == "" {
		return ""
	}

	matched := make(map[int]struct{}, len(matchedIndexes))
	for _, idx := range matchedIndexes {
		matched[idx] = struct{}{}
	}

	var b strings.Builder
	for idx, r := range text {
		ch := string(r)
		if _, ok := matched[idx]; ok {
			b.WriteString(theme.HighlightMatch(ch, matchedColor))
			continue
		}
		b.WriteString(baseStyle.Render(ch))
	}
	return b.String()
}

func filterSearchPickerEntries[T any](allEntries []T, query string, matches func(T, string) bool) []T {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return append([]T(nil), allEntries...)
	}
	result := make([]T, 0, len(allEntries))
	for _, entry := range allEntries {
		if matches(entry, q) {
			result = append(result, entry)
		}
	}
	return result
}
