package tui

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tui/theme"
)

const maxDisplay = 8

type filePickerOverlay struct {
	OverlayShell
	root         string
	query        string
	allEntries   []string
	candidates   []string
	matchIndexes [][]int
	selection    int
	scrollOffset int
	styles       theme.Styles
}

type searchPickerUpdateResult int

const (
	searchPickerIgnored searchPickerUpdateResult = iota
	searchPickerHandled
	searchPickerClosed
)

func newFilePickerOverlay(styles theme.Styles) filePickerOverlay {
	return filePickerOverlay{styles: styles}
}

func (f filePickerOverlay) Open(root string) filePickerOverlay {
	f.OverlayShell = f.openShell()
	f.root = root
	f.query = ""
	f.selection = 0
	f.scrollOffset = 0
	f.allEntries = nil
	f.candidates = nil
	f.matchIndexes = nil

	excluder := tool.NewPathExcluderWithIncludes(nil, nil, tool.FilePickerAlwaysInclude)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if excluder.ShouldExclude(rel) {
				return filepath.SkipDir
			}
			f.allEntries = append(f.allEntries, rel+"/")
		} else {
			if excluder.ShouldExclude(rel) {
				return nil
			}
			f.allEntries = append(f.allEntries, rel)
		}
		return nil
	})
	if err != nil {
		f.allEntries = nil
	}

	f.candidates = append([]string(nil), f.allEntries...)
	f.matchIndexes = make([][]int, len(f.candidates))
	return f
}

func (f filePickerOverlay) Close() filePickerOverlay {
	f.OverlayShell = f.closeShell()
	return f
}

func (f *filePickerOverlay) syncQuery(query string) {
	f.query = query
	f.filter()
}

func (f filePickerOverlay) Update(msg tea.Msg) (filePickerOverlay, tea.Cmd) {
	if !f.IsOpen() {
		return f, nil
	}
	switch updateSearchPicker(&f.query, &f.selection, &f.scrollOffset, &f.candidates, f.allEntries, msg, func(query string, entries []string) []string {
		results, matchIndexes := fuzzyMatchStrings(entries, query)
		f.matchIndexes = matchIndexes
		return results
	}) {
	case searchPickerClosed:
		return f.Close(), nil
	case searchPickerHandled, searchPickerIgnored:
		return f, nil
	default:
		return f, nil
	}
}

func (f *filePickerOverlay) filter() {
	f.scrollOffset = 0
	f.selection = 0
	f.candidates, f.matchIndexes = fuzzyMatchStrings(f.allEntries, f.query)
}

func (f filePickerOverlay) View() string {
	if !f.IsOpen() {
		return ""
	}

	innerWidth := f.filePickerInnerWidth()
	const selectionPrefix = "> "
	const idlePrefix = "  "

	prefix := f.styles.Accent.Render("@")
	queryDisplay := f.query
	if queryDisplay == "" {
		if len(f.candidates) > 0 {
			queryDisplay = f.styles.Accent.Render(f.candidates[f.selection])
		} else {
			queryDisplay = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render("search files…")
		}
	}
	headerLine := lipgloss.NewStyle().Width(innerWidth).Render(prefix + " " + queryDisplay)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", innerWidth))

	lines := []string{headerLine, divider}

	dirStyle := f.styles.Accent
	fileStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))

	for i := f.scrollOffset; i < min(f.scrollOffset+maxDisplay, len(f.candidates)); i++ {
		entry := f.candidates[i]
		matchedIndexes := []int(nil)
		if i < len(f.matchIndexes) {
			matchedIndexes = f.matchIndexes[i]
		}
		var row string
		if strings.HasSuffix(entry, "/") {
			row = renderMatchedText(entry, matchedIndexes, dirStyle, f.styles.AccentColor)
		} else {
			row = renderMatchedText(entry, matchedIndexes, fileStyle, f.styles.AccentColor)
		}
		if i == f.selection {
			lines = append(lines, f.styles.PaletteItemActive.
				Width(innerWidth).
				MaxWidth(innerWidth).
				Render(f.styles.Accent.Render(selectionPrefix)+row))
		} else {
			lines = append(lines, lipgloss.NewStyle().
				Width(innerWidth).
				MaxWidth(innerWidth).
				Render(idlePrefix+row))
		}
	}

	if len(f.candidates) > f.scrollOffset+maxDisplay {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render(fmt.Sprintf("... and %d more", len(f.candidates)-(f.scrollOffset+maxDisplay))))
	}

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	rendered := f.styles.PaletteOverlay.Width(innerWidth+2).Padding(1, 1).Render(body)
	return theme.WithBg(rendered, lipgloss.Color(theme.BgElev))
}

func (f filePickerOverlay) filePickerInnerWidth() int {
	const maxOverlayInner = 90
	inner := f.InnerWidth()
	if inner > maxOverlayInner {
		inner = maxOverlayInner
	}
	if inner < 40 {
		inner = 40
	}
	return inner
}

func updateSearchPicker[T any](query *string, selection *int, scrollOffset *int, candidates *[]T, allEntries []T, msg tea.Msg, filter func(string, []T) []T) searchPickerUpdateResult {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return searchPickerIgnored
	}

	switch keyMsg.Code {
	case tea.KeyEsc:
		return searchPickerClosed
	case tea.KeyEnter:
		return searchPickerHandled
	case tea.KeyUp:
		if *selection > 0 {
			*selection--
		}
		scrollSearchPickerIntoView(selection, scrollOffset)
		return searchPickerHandled
	case tea.KeyDown:
		if *selection < len(*candidates)-1 {
			*selection++
		}
		scrollSearchPickerIntoView(selection, scrollOffset)
		return searchPickerHandled
	case tea.KeyBackspace:
		if len(*query) > 0 {
			*query = (*query)[:len(*query)-1]
			*candidates = filter(*query, allEntries)
			*selection = 0
			*scrollOffset = 0
		}
		return searchPickerHandled
	}
	// Handle printable characters (tea.KeyRunes equivalent)
	if keyMsg.Text != "" {
		*query += keyMsg.String()
		*candidates = filter(*query, allEntries)
		*selection = 0
		*scrollOffset = 0
		return searchPickerHandled
	}
	return searchPickerIgnored
}

func scrollSearchPickerIntoView(selection *int, scrollOffset *int) {
	if *selection >= *scrollOffset+maxDisplay {
		*scrollOffset = *selection - maxDisplay + 1
	}
	if *selection < *scrollOffset {
		*scrollOffset = *selection
	}
	if *scrollOffset < 0 {
		*scrollOffset = 0
	}
}
