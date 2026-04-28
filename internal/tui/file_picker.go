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

type filePickerOverlay struct {
	open       bool
	root       string
	query      string
	allEntries []string
	candidates []string
	selection  int
	styles     theme.Styles
	width      int
	height     int
}

func newFilePickerOverlay(styles theme.Styles) filePickerOverlay {
	return filePickerOverlay{styles: styles}
}

func (f filePickerOverlay) Open(root string) filePickerOverlay {
	f.open = true
	f.root = root
	f.query = ""
	f.selection = 0
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
	f.open = false
	return f
}

func (f filePickerOverlay) Update(msg tea.Msg) (filePickerOverlay, tea.Cmd) {
	if !f.open {
		return f, nil
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return f, nil
	}
	switch keyMsg.Type {
	case tea.KeyEsc:
		return f.Close(), nil
	case tea.KeyEnter:
		return f, nil
	case tea.KeyUp:
		if f.selection > 0 {
			f.selection--
		}
		return f, nil
	case tea.KeyDown:
		if f.selection < len(f.candidates)-1 {
			f.selection++
		}
		return f, nil
	case tea.KeyBackspace:
		if len(f.query) > 0 {
			f.query = f.query[:len(f.query)-1]
			f.filter()
		}
		return f, nil
	case tea.KeyRunes:
		f.query += keyMsg.String()
		f.filter()
		f.selection = 0
		return f, nil
	}
	return f, nil
}

func (f *filePickerOverlay) filter() {
	q := strings.ToLower(f.query)
	if q == "" {
		f.candidates = append([]string(nil), f.allEntries...)
		return
	}
	var result []string
	for _, entry := range f.allEntries {
		if strings.Contains(strings.ToLower(entry), q) {
			result = append(result, entry)
		}
	}
	f.candidates = result
}

func (f filePickerOverlay) View() string {
	if !f.open {
		return ""
	}

	overlayWidth := f.width - 4
	if overlayWidth < 40 {
		overlayWidth = 40
	}
	innerWidth := overlayWidth - 4

	prefix := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.AccentAmber)).Render("@")
	queryDisplay := f.query
	if queryDisplay == "" {
		queryDisplay = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render("search files…")
	}
	headerLine := lipgloss.NewStyle().Width(innerWidth).Render(prefix + " " + queryDisplay)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", innerWidth))

	maxDisplay := 8
	lines := []string{headerLine, divider}

	dirStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.AccentAmber))
	fileStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))

	for i, entry := range f.candidates {
		if i >= maxDisplay {
			break
		}
		row := entry
		if strings.HasSuffix(entry, "/") {
			row = dirStyle.Render(entry)
		} else {
			row = fileStyle.Render(entry)
		}
		if i == f.selection {
			lines = append(lines, f.styles.PaletteItemActive.Width(innerWidth).Render(row))
		} else {
			lines = append(lines, lipgloss.NewStyle().Width(innerWidth).Render(row))
		}
	}

	if len(f.candidates) > maxDisplay {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render(fmt.Sprintf("... and %d more", len(f.candidates)-maxDisplay)))
	}

	footerDivider := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", innerWidth))
	chip := func(k string) string {
		return lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev2)).Foreground(lipgloss.Color(theme.FgFaint)).Padding(0, 1).Render(k)
	}
	footerText := chip("↵") + " select   " + chip("↑↓") + " navigate   " + chip("esc") + " close"
	footerLine := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Width(innerWidth).Render(footerText)
	lines = append(lines, footerDivider, footerLine)

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	box := f.styles.PaletteOverlay.
		Width(innerWidth).
		Padding(1, 1).
		Render(body)

	return box
}
