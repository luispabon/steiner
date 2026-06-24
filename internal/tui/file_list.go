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

type fileListOverlay struct {
	OverlayShell
	root    string
	entries []string
	styles  theme.Styles
}

func newFileListOverlay(styles theme.Styles) fileListOverlay {
	return fileListOverlay{
		OverlayShell: OverlayShell{}.WithPreferredWidth(70),
		styles:       styles,
	}
}

func (f fileListOverlay) Open(root string) fileListOverlay {
	f.OverlayShell = f.openShell()
	f.root = root
	f.entries = nil

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
		f.entries = []string{fmt.Sprintf("error: %v", err)}
	}

	return f
}

func (f fileListOverlay) Close() fileListOverlay {
	f.OverlayShell = f.closeShell()
	return f
}

func (f fileListOverlay) View() string {
	if !f.IsOpen() {
		return ""
	}

	header := fmt.Sprintf("Files: %s", f.root)
	divider := f.Divider()

	maxDisplay := 30
	var lines []string
	switch {
	case len(f.entries) > 0 && strings.HasPrefix(f.entries[0], "error:"):
		lines = append(lines, f.styles.ErrorStyle.Render(f.entries[0]))
	case len(f.entries) == 0:
		lines = append(lines, "(empty)")
	default:
		dirStyle := f.styles.Accent
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

	footerDivider := f.Divider()
	footer := f.RenderFooter(fmt.Sprintf("esc/enter to close  |  %d entries", len(f.entries)))

	body := lipgloss.JoinVertical(lipgloss.Left,
		header,
		divider,
		lipgloss.JoinVertical(lipgloss.Left, lines...),
		footerDivider,
		footer,
	)
	return f.RenderWithBg(f.styles.PaletteOverlay, body)
}

func (f fileListOverlay) Update(msg tea.Msg) (fileListOverlay, tea.Cmd) {
	if !f.IsOpen() {
		return f, nil
	}
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return f, nil
	}
	switch keyMsg.Code {
	case tea.KeyEsc, tea.KeyEnter:
		return f.Close(), nil
	}
	return f, nil
}
