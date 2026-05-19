package tui

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

	excluder := tool.NewPathExcluder(nil, nil)
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
	return f
}

func (f filePickerOverlay) Close() filePickerOverlay {
	f.OverlayShell = f.closeShell()
	return f
}

func (f filePickerOverlay) Update(msg tea.Msg) (filePickerOverlay, tea.Cmd) {
	if !f.open {
		return f, nil
	}
	switch updateSearchPicker(&f.query, &f.selection, &f.scrollOffset, &f.candidates, f.allEntries, msg, func(query string, entries []string) []string {
		return filterSearchPickerEntries(entries, query, func(entry string, loweredQuery string) bool {
			return strings.Contains(strings.ToLower(entry), loweredQuery)
		})
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
	f.candidates = filterSearchPickerEntries(f.allEntries, f.query, func(entry string, loweredQuery string) bool {
		return strings.Contains(strings.ToLower(entry), loweredQuery)
	})
}

func (f filePickerOverlay) View() string {
	if !f.open {
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
	divider := f.Divider()

	lines := []string{headerLine, divider}

	dirStyle := f.styles.Accent
	fileStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))

	for i := f.scrollOffset; i < min(f.scrollOffset+maxDisplay, len(f.candidates)); i++ {
		entry := f.candidates[i]
		var row string
		if strings.HasSuffix(entry, "/") {
			row = dirStyle.Render(entry)
		} else {
			row = fileStyle.Render(entry)
		}
		if i == f.selection {
			lines = append(lines, lipgloss.NewStyle().
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
	rendered := f.styles.PaletteOverlay.Width(innerWidth).Padding(1, 1).Render(body)
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

func updateSearchPicker[T any](query *string, selection *int, scrollOffset *int, candidates *[]T, allEntries []T, msg tea.Msg, filter func(string, []T) []T) searchPickerUpdateResult {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return searchPickerIgnored
	}

	switch keyMsg.Type {
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
	case tea.KeyRunes:
		*query += keyMsg.String()
		*candidates = filter(*query, allEntries)
		*selection = 0
		*scrollOffset = 0
		return searchPickerHandled
	default:
		return searchPickerIgnored
	}
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
