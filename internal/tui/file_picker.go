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

func newFilePickerOverlay(styles theme.Styles) filePickerOverlay {
	return filePickerOverlay{styles: styles}
}

func (f filePickerOverlay) Open(root string) filePickerOverlay {
	f.OverlayShell = f.OverlayShell.openShell()
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
	f.OverlayShell = f.OverlayShell.closeShell()
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
		f.scrollIntoView()
		return f, nil
	case tea.KeyDown:
		if f.selection < len(f.candidates)-1 {
			f.selection++
		}
		f.scrollIntoView()
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
	f.scrollOffset = 0
	f.selection = 0
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

	innerWidth := f.InnerWidth()

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
		row := entry
		if strings.HasSuffix(entry, "/") {
			row = dirStyle.Render(entry)
		} else {
			row = fileStyle.Render(entry)
		}
		if i == f.selection {
			lines = append(lines, f.styles.AccentBg.MaxWidth(innerWidth).Render(row))
		} else {
			lines = append(lines, lipgloss.NewStyle().MaxWidth(innerWidth).Render(row))
		}
	}

	if len(f.candidates) > f.scrollOffset+maxDisplay {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render(fmt.Sprintf("... and %d more", len(f.candidates)-(f.scrollOffset+maxDisplay))))
	}

	footerText := FooterChip("↵") + " select   " + FooterChip("↑↓") + " navigate   " + FooterChip("esc") + " close"
	lines = append(lines, f.Divider(), f.RenderFooter(footerText))

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return theme.WithBg(f.Render(overlayStyles{box: f.styles.PaletteOverlay}, body), lipgloss.Color(theme.BgElev))
}

func (f *filePickerOverlay) scrollIntoView() {
	if f.selection >= f.scrollOffset+maxDisplay {
		f.scrollOffset = f.selection - maxDisplay + 1
	}
	if f.selection < f.scrollOffset {
		f.scrollOffset = f.selection
	}
}
