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

type fileListOverlay struct {
	open    bool
	root    string
	entries []string
	err     string
	styles  theme.Styles
	width   int
	height  int
}

func newFileListOverlay(styles theme.Styles) fileListOverlay {
	return fileListOverlay{styles: styles}
}

func (f fileListOverlay) Open(root string) fileListOverlay {
	f.open = true
	f.root = root
	f.err = ""
	f.entries = nil

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
			f.entries = append(f.entries, rel+"/")
		} else {
			if excluder.ShouldExclude(rel) {
				return nil
			}
			f.entries = append(f.entries, rel)
		}
		return nil
	})
	if err != nil {
		f.err = fmt.Sprintf("error: %v", err)
	}

	return f
}

func (f fileListOverlay) Close() fileListOverlay {
	f.open = false
	return f
}

func (f fileListOverlay) View() string {
	if !f.open {
		return ""
	}

	overlayWidth := 70
	if overlayWidth > f.width-4 {
		overlayWidth = f.width - 4
	}
	if overlayWidth < 40 {
		overlayWidth = 40
	}
	innerWidth := overlayWidth - 4

	header := fmt.Sprintf("Files: %s", f.root)
	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.BorderSoft)).
		Render(strings.Repeat("─", innerWidth))

	maxDisplay := 30
	var lines []string
	if f.err != "" {
		lines = append(lines, f.styles.ErrorStyle.Render(f.err))
	} else if len(f.entries) == 0 {
		lines = append(lines, "(empty)")
	} else {
		dirStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.AccentAmber))
		fileStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
		displayCount := min(len(f.entries), maxDisplay)
		for _, entry := range f.entries[:displayCount] {
			if strings.HasSuffix(entry, "/") {
				lines = append(lines, dirStyle.Render(entry))
			} else {
				lines = append(lines, fileStyle.Render(entry))
			}
		}
		if len(f.entries) > maxDisplay {
			lines = append(lines, lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.FgMute)).
				Render(fmt.Sprintf("... and %d more", len(f.entries)-maxDisplay)))
		}
	}

	footerDivider := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.BorderSoft)).
		Render(strings.Repeat("─", innerWidth))
	footer := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.FgMute)).
		Width(innerWidth).
		Render(fmt.Sprintf("esc/enter to close  |  %d entries", len(f.entries)))

	body := lipgloss.JoinVertical(lipgloss.Left,
		header,
		divider,
		lipgloss.JoinVertical(lipgloss.Left, lines...),
		footerDivider,
		footer,
	)
	box := f.styles.PaletteOverlay.
		Width(innerWidth).
		Padding(1, 1).
		Render(body)

	return box
}

func (f fileListOverlay) Update(msg tea.Msg) (fileListOverlay, tea.Cmd) {
	if !f.open {
		return f, nil
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return f, nil
	}
	switch keyMsg.Type {
	case tea.KeyEsc, tea.KeyEnter:
		return f.Close(), nil
	}
	return f, nil
}
