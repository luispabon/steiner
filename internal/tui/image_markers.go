package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
)

// tuiFormatSize returns a human-readable size string like "2KB" or "1.5MB".
func tuiFormatSize(sizeBytes int) string {
	const (
		kb = 1024
		mb = 1024 * kb
	)
	switch {
	case sizeBytes >= mb:
		return fmt.Sprintf("%.1fMB", float64(sizeBytes)/mb)
	case sizeBytes >= kb:
		return fmt.Sprintf("%dKB", sizeBytes/kb)
	default:
		return fmt.Sprintf("%dB", sizeBytes)
	}
}

type imageMarker struct {
	label string
	image agent.ImageBlock
}

var imageMarkerPattern = regexp.MustCompile(`\[Image \d+\]`)

// partialMarkerPattern matches incomplete marker fragments that should be cleaned up.
var partialMarkerPattern = regexp.MustCompile(`\[Image\s*\d*\]?`)

func nextMarkerLabel(markers []imageMarker) string {
	return fmt.Sprintf("[Image %d]", len(markers)+1)
}

func pendingImageBlocks(markers []imageMarker) []agent.ImageBlock {
	if len(markers) == 0 {
		return nil
	}
	out := make([]agent.ImageBlock, len(markers))
	for i, m := range markers {
		out[i] = m.image
	}
	return out
}

func removeMarkerFromValue(value string, marker imageMarker) string {
	return strings.Replace(value, marker.label, "", 1)
}

func renumberMarkers(value string, markers []imageMarker) (string, []imageMarker) {
	locs := imageMarkerPattern.FindAllStringIndex(value, -1)
	if len(locs) != len(markers) {
		return value, markers
	}
	// Build a map from old label → marker so we can reorder by text position.
	byLabel := make(map[string]imageMarker, len(markers))
	for _, m := range markers {
		byLabel[m.label] = m
	}
	newMarkers := make([]imageMarker, 0, len(markers))
	newValue := value
	// We replace right-to-left to keep byte offsets valid.
	for i := len(locs) - 1; i >= 0; i-- {
		newLabel := fmt.Sprintf("[Image %d]", i+1)
		start, end := locs[i][0], locs[i][1]
		newValue = newValue[:start] + newLabel + newValue[end:]
	}
	// Rebuild markers in text order (left-to-right) with new labels.
	for i, loc := range locs {
		oldLabel := value[loc[0]:loc[1]]
		m, ok := byLabel[oldLabel]
		if !ok {
			return value, markers
		}
		m.label = fmt.Sprintf("[Image %d]", i+1)
		newMarkers = append(newMarkers, m)
	}
	return newValue, newMarkers
}

func cursorRuneOffset(value string, row, col int) int {
	lines := strings.Split(value, "\n")
	total := 0
	for i, line := range lines {
		if i == row {
			runes := []rune(line)
			if col > len(runes) {
				col = len(runes)
			}
			return total + col
		}
		total += len([]rune(line)) + 1 // +1 for newline
	}
	return total
}

func markerAtCursor(value string, runeOffset int, markers []imageMarker) (idx int, atStart, atEnd, inside bool) {
	locs := imageMarkerPattern.FindAllStringIndex(value, -1)
	for i, loc := range locs {
		startRune := len([]rune(value[:loc[0]]))
		endRune := startRune + len([]rune(value[loc[0]:loc[1]]))

		label := value[loc[0]:loc[1]]
		markerIdx := -1
		for j, m := range markers {
			if m.label == label {
				markerIdx = j
				break
			}
		}
		if markerIdx == -1 {
			markerIdx = i
		}

		if runeOffset == startRune {
			return markerIdx, true, false, false
		}
		if runeOffset == endRune {
			return markerIdx, false, true, false
		}
		if runeOffset > startRune && runeOffset < endRune {
			return markerIdx, false, false, true
		}
	}
	return -1, false, false, false
}

func reconcileMarkers(value string, markers []imageMarker) (string, []imageMarker) {
	// Remove partial/incomplete marker fragments that don't match the full pattern.
	// Find all partial matches, then remove those that aren't full matches.
	cleaned := partialMarkerPattern.ReplaceAllStringFunc(value, func(s string) string {
		if imageMarkerPattern.MatchString(s) {
			return s
		}
		return ""
	})

	// Keep only markers whose labels still exist in the cleaned value.
	survivors := make([]imageMarker, 0, len(markers))
	for _, m := range markers {
		if strings.Contains(cleaned, m.label) {
			survivors = append(survivors, m)
		}
	}

	if len(survivors) == 0 {
		return cleaned, nil
	}

	cleaned, survivors = renumberMarkers(cleaned, survivors)
	return cleaned, survivors
}

func snapCursorPastMarkers(value string, runeOffset int, _ []imageMarker, direction int) int {
	locs := imageMarkerPattern.FindAllStringIndex(value, -1)
	for _, loc := range locs {
		startRune := len([]rune(value[:loc[0]]))
		endRune := startRune + len([]rune(value[loc[0]:loc[1]]))
		if runeOffset > startRune && runeOffset < endRune {
			if direction < 0 {
				return startRune
			}
			return endRune
		}
	}
	return runeOffset
}

func (m *Model) pendingImageBlocks() []agent.ImageBlock {
	return pendingImageBlocks(m.imageMarkers)
}

func (m *Model) restoreCursorFromRuneOffset(value string, targetRuneOff int) {
	lines := strings.Split(value, "\n")
	remaining := targetRuneOff
	for row, line := range lines {
		lineRunes := len([]rune(line))
		if remaining <= lineRunes {
			m.input.SetValue(value)
			m.input.CursorStart()
			for m.input.Line() > 0 {
				m.input.CursorUp()
			}
			for i := 0; i < row; i++ {
				m.input.CursorDown()
			}
			m.input.SetCursorColumn(remaining)
			return
		}
		remaining -= lineRunes + 1 // +1 for newline
	}
	m.input.SetValue(value)
}

func (m *Model) cursorCol() int {
	li := m.input.LineInfo()
	return li.StartColumn + li.ColumnOffset
}
