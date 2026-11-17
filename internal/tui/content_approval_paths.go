package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
)

// approvalPathReplacer keeps an outside path on one display line. Sanitizing
// leaves newlines and tabs in place, so both are folded to spaces.
var approvalPathReplacer = strings.NewReplacer("\n", " ", "\t", " ", "\r", " ")

// approvalOutsidePaths returns sanitized, width-wrapped lines for the outside
// paths a path approval covers, ending with a remainder line when the collected
// list is shorter than the total count. It returns nil when there is nothing to show.
func approvalOutsidePaths(paths []string, count, width int, style lipgloss.Style) []string {
	if width < 1 {
		return nil
	}
	if len(paths) == 0 && count <= 0 {
		return nil
	}
	lines := make([]string, 0, len(paths)+1)
	shown := 0
	for _, path := range paths {
		// Paths are model-supplied, so each is sanitized onto a single line
		// before it is wrapped to the available width.
		line := strings.TrimSpace(approvalPathReplacer.Replace(output.SanitizeDisplayText(path)))
		if line == "" {
			continue
		}
		shown++
		lines = append(lines, wrapStyledApprovalPath(line, width, style)...)
	}
	if remaining := count - shown; remaining > 0 {
		lines = append(lines, wrapStyledApprovalPath(fmt.Sprintf("and %d more outside the project", remaining), width, style)...)
	}
	if len(lines) == 0 {
		return nil
	}
	return lines
}

// wrapStyledApprovalPath wraps one path line to width and splits the styled
// result back into lines, mirroring wrapStyledDelegationLines.
func wrapStyledApprovalPath(line string, width int, style lipgloss.Style) []string {
	wrapped := style.Width(width).Render(line)
	return strings.Split(strings.TrimRight(wrapped, "\n"), "\n")
}
