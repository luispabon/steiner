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

var imageMarkerPattern = regexp.MustCompile(`\[img-\d+\]`)

// Require an image number before treating a fragment as an incomplete marker.
var partialMarkerPattern = regexp.MustCompile(`\[img-\d+\]?`)

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

// markerLabel returns the composer marker label for block, stamping a
// TUI-local img-N ID when the block has none. Blocks registered with the image
// store carry a stable ID; the local counter only runs when no store is wired
// (tests), where no store IDs exist to collide with.
func (m *Model) markerLabel(block *agent.ImageBlock) string {
	if block.ID == "" {
		m.localImageCounter++
		block.ID = fmt.Sprintf("img-%d", m.localImageCounter)
	}
	return "[" + block.ID + "]"
}

func hasMarkerLabel(markers []imageMarker, label string) bool {
	for _, m := range markers {
		if m.label == label {
			return true
		}
	}
	return false
}

// pendingMarkerLabels returns the label set of every pending marker, used to
// distinguish real markers from user-typed marker-shaped text.
func (m *Model) pendingMarkerLabels() map[string]struct{} {
	if len(m.imageMarkers) == 0 {
		return nil
	}
	labels := make(map[string]struct{}, len(m.imageMarkers))
	for _, mk := range m.imageMarkers {
		labels[mk.label] = struct{}{}
	}
	return labels
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
	for _, loc := range imageMarkerPattern.FindAllStringIndex(value, -1) {
		label := value[loc[0]:loc[1]]
		markerIdx := -1
		for j, m := range markers {
			if m.label == label {
				markerIdx = j
				break
			}
		}
		if markerIdx == -1 {
			// Marker-shaped text with no pending marker is plain user text.
			continue
		}

		startRune := len([]rune(value[:loc[0]]))
		endRune := startRune + len([]rune(label))
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

	return cleaned, survivors
}

func snapCursorPastMarkers(value string, runeOffset int, markers []imageMarker, direction int) int {
	for _, loc := range imageMarkerPattern.FindAllStringIndex(value, -1) {
		label := value[loc[0]:loc[1]]
		if !hasMarkerLabel(markers, label) {
			continue
		}
		startRune := len([]rune(value[:loc[0]]))
		endRune := startRune + len([]rune(label))
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

// removeStoreImage forgets a marker's image in the store, tolerating no store
// and empty IDs.
func (m *Model) removeStoreImage(mk imageMarker) {
	if m.imageStore != nil && mk.image.ID != "" {
		m.imageStore.Remove(mk.image.ID)
	}
}

// removePendingImages discards every pending image, deleting each from the
// store because IDs are never reused. Used on any non-submit discard pathway.
func (m *Model) removePendingImages() {
	for _, mk := range m.imageMarkers {
		m.removeStoreImage(mk)
	}
	m.imageMarkers = nil
}

// removeVanishedMarkers deletes the store file of every current marker that is
// absent from survivors (e.g. edited out of the composer).
func (m *Model) removeVanishedMarkers(survivors []imageMarker) {
	for _, mk := range m.imageMarkers {
		if !hasMarkerLabel(survivors, mk.label) {
			m.removeStoreImage(mk)
		}
	}
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
