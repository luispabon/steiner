package tui

import (
	"image/color"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
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

// scoreStringMatch computes a custom relevance score for a fuzzy match.
// Higher scores indicate better matches. Exact and prefix substring matches
// are heavily boosted, followed by consecutive runs, word boundaries, and
// early match position.
func scoreStringMatch(str string, lowerQuery string, matchedIndexes []int) int {
	if len(matchedIndexes) == 0 {
		return 0
	}

	lowerStr := strings.ToLower(str)
	score := 0

	// Exact substring bonus
	if strings.Contains(lowerStr, lowerQuery) {
		score += 100000
		// Prefix bonus
		if strings.HasPrefix(lowerStr, lowerQuery) {
			score += 50000
		}
	}

	// Consecutive character run bonus
	score += consecutiveScore(matchedIndexes) * 100

	// Word boundary / first character bonuses
	for _, idx := range matchedIndexes {
		if idx == 0 {
			score += 1000
			continue
		}
		switch str[idx-1] {
		case ' ', '_', '-', '/', '.', '\\':
			score += 300
		}
	}

	// Penalize late first match
	score -= matchedIndexes[0] * 10

	return score
}

// consecutiveScore rewards contiguous matched characters. Longer runs
// produce quadratically higher scores.
func consecutiveScore(indexes []int) int {
	if len(indexes) == 0 {
		return 0
	}
	score := 0
	runLen := 1
	for i := 1; i < len(indexes); i++ {
		if indexes[i] == indexes[i-1]+1 {
			runLen++
		} else {
			score += runLen * runLen
			runLen = 1
		}
	}
	score += runLen * runLen
	return score
}

func fuzzyMatchStrings(entries []string, query string) ([]string, [][]int) {
	q := strings.TrimSpace(query)
	if q == "" {
		return append([]string(nil), entries...), make([][]int, len(entries))
	}

	matches := fuzzy.Find(q, entries)

	type scoredMatch struct {
		match fuzzy.Match
		score int
	}

	scored := make([]scoredMatch, len(matches))
	lowerQ := strings.ToLower(q)
	for i, match := range matches {
		scored[i] = scoredMatch{
			match: match,
			score: scoreStringMatch(entries[match.Index], lowerQ, match.MatchedIndexes),
		}
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	results := make([]string, 0, len(matches))
	indexes := make([][]int, 0, len(matches))
	for _, s := range scored {
		results = append(results, entries[s.match.Index])
		indexes = append(indexes, append([]int(nil), s.match.MatchedIndexes...))
	}
	return results, indexes
}

// scoreSlashMatch computes a field-aware relevance score for slash overlay items.
// Matches in the command field are weighted highest, followed by name, then description.
func scoreSlashMatch(item slashOverlayItem, query string, matchData slashOverlayMatch) int {
	lowerQ := strings.ToLower(query)
	score := 0

	// Command field scoring — highest priority
	if len(matchData.commandIndexes) > 0 {
		lowerCmd := strings.ToLower(item.command)
		if strings.Contains(lowerCmd, lowerQ) {
			score += 500000
			if strings.HasPrefix(lowerCmd, lowerQ) {
				score += 250000
			} else if strings.HasPrefix(lowerCmd, "/"+lowerQ) {
				score += 200000
			}
		}
		score += consecutiveScore(matchData.commandIndexes) * 10

		for _, idx := range matchData.commandIndexes {
			if idx == 0 {
				score += 2000
				continue
			}
			if idx == 1 && item.command[0] == '/' {
				score += 2000
				continue
			}
			switch item.command[idx-1] {
			case ' ', '_', '-':
				score += 500
			}
		}
	}

	// Name field scoring — medium priority
	if len(matchData.nameIndexes) > 0 {
		lowerName := strings.ToLower(item.name)
		if strings.Contains(lowerName, lowerQ) {
			score += 100000
			if strings.HasPrefix(lowerName, lowerQ) {
				score += 50000
			}
		}
		score += consecutiveScore(matchData.nameIndexes) * 5
	}

	// Description field scoring — lowest priority
	if len(matchData.descIndexes) > 0 {
		lowerDesc := strings.ToLower(item.desc)
		if strings.Contains(lowerDesc, lowerQ) {
			score += 50000
		}
		score += consecutiveScore(matchData.descIndexes)
	}

	return score
}

func fuzzyMatchSlashItems(items []slashOverlayItem, query string) ([]slashOverlayItem, []slashOverlayMatch) {
	q := strings.TrimSpace(strings.TrimPrefix(query, "/"))
	if q == "" {
		return append([]slashOverlayItem(nil), items...), make([]slashOverlayMatch, len(items))
	}

	matches := fuzzy.FindFrom(q, slashOverlaySource(items))

	type scoredSlashMatch struct {
		match     fuzzy.Match
		matchData slashOverlayMatch
		score     int
	}

	scored := make([]scoredSlashMatch, len(matches))
	for i, match := range matches {
		item := items[match.Index]
		matchData := splitSlashOverlayMatch(item, match.MatchedIndexes)
		scored[i] = scoredSlashMatch{
			match:     match,
			matchData: matchData,
			score:     scoreSlashMatch(item, q, matchData),
		}
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	results := make([]slashOverlayItem, 0, len(matches))
	matchData := make([]slashOverlayMatch, 0, len(matches))
	for _, s := range scored {
		results = append(results, items[s.match.Index])
		matchData = append(matchData, s.matchData)
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

func renderMatchedText(text string, matchedIndexes []int, baseStyle lipgloss.Style, matchedColor color.Color) string {
	if text == "" {
		return ""
	}

	matched := make(map[int]struct{}, len(matchedIndexes))
	for _, idx := range matchedIndexes {
		matched[idx] = struct{}{}
	}

	colorHex := theme.ColorHex(matchedColor)
	var b strings.Builder
	for idx, r := range text {
		ch := string(r)
		if _, ok := matched[idx]; ok {
			b.WriteString(theme.HighlightMatch(ch, colorHex))
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
