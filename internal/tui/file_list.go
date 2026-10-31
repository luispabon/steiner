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
	styles  *theme.Styles
}

func newFileListOverlay(styles *theme.Styles) fileListOverlay {
	return fileListOverlay{
		OverlayShell: OverlayShell{}.WithPreferredWidth(70),
		styles:       styles,
	}
}

func (f fileListOverlay) Open(root string) fileListOverlay {
	f.OverlayShell = f.openShell()
	f.root = root

	entries, err := walkDirEntries(root)
	if err != nil {
		f.entries = []string{fmt.Sprintf("error: %v", err)}
	} else {
		f.entries = entries
	}

	return f
}

// walkDirEntries walks root and returns relative paths, with directories
// suffixed by "/". A symlinked root is resolved before the walk, because
// filepath.WalkDir lstats the root and would otherwise report the symlink
// itself instead of descending into it. The returned paths stay lexical,
// relative to the root the caller passed. Symlinks nested inside the root are
// not followed: they are listed as file entries. It uses the file picker
// excluder, anchored to the resolved root: the picker force-includes .steiner
// but exact-excludes its heavy subdirectories (tmp, worktrees), and built-in
// directory names apply relative to the resolved root.
func walkDirEntries(root string) ([]string, error) {
	walkRoot := resolveWalkRoot(root)
	excluder := tool.NewWorkspaceExcluderWithIncludes(walkRoot, tool.FilePickerExcludePaths, nil, tool.FilePickerAlwaysInclude)
	var entries []string
	err := filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == walkRoot {
				return err
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(walkRoot, path)
		if err != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if excluder.ExcludeEntry(walkRoot, path, true) {
				return filepath.SkipDir
			}
			entries = append(entries, rel+"/")
		} else {
			if excluder.ExcludeEntry(walkRoot, path, false) {
				return nil
			}
			entries = append(entries, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// resolveWalkRoot resolves symlinks in root so the walk descends into a
// symlinked root. The original root is returned when resolution fails, leaving
// walkDirEntries to report the underlying access error.
func resolveWalkRoot(root string) string {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return root
	}
	return resolved
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
			lines = append(lines, renderMoreFooter(len(f.entries)-maxDisplay))
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
	return f.RenderWithBg(f.styles.PaletteOverlay, body, theme.BgElev)
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
