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

// maxApprovalOutsidePathLines bounds the outside-path block, remainder line
// included. The embedded tool body caps its own rows, and an uncapped path
// block pushes the decision buttons past the viewport, where a user can approve
// without ever seeing the list. The block therefore keeps only the paths that
// fit the budget and counts the rest in its remainder line.
const maxApprovalOutsidePathLines = 12

// approvalOutsidePaths returns sanitized, width-wrapped lines for the outside
// paths a path approval covers, ending with a remainder line when the collected
// list is shorter than the total count or does not fit the line budget. Paths
// stay in order, a path is either shown whole or counted in the remainder, and
// the result never exceeds maxApprovalOutsidePathLines. It returns nil when
// there is nothing to show.
func approvalOutsidePaths(paths []string, count, width int, style lipgloss.Style) []string {
	if width < 1 {
		return nil
	}

	wrapped := wrapApprovalPaths(paths, width, style)
	if len(wrapped) == 0 && count <= 0 {
		return nil
	}
	shown, used := fitApprovalPaths(wrapped, maxApprovalOutsidePathLines)
	if shown == len(wrapped) && count <= shown {
		lines := make([]string, 0, used)
		for _, pathLines := range wrapped {
			lines = append(lines, pathLines...)
		}
		return lines
	}

	// Something is left out, so the wrapped remainder line shares the same
	// budget and paths move to the remainder count to make room for it.
	shown, used, remainder := approvalRemainderLines(wrapped, shown, used, count, width, style)
	if room := maxApprovalOutsidePathLines - used; len(remainder) > room {
		remainder = remainder[:room]
	}

	lines := make([]string, 0, used+len(remainder))
	for _, pathLines := range wrapped[:shown] {
		lines = append(lines, pathLines...)
	}
	return append(lines, remainder...)
}

// wrapApprovalPaths sanitizes each path onto a single line and wraps it to
// width, dropping paths that sanitize to nothing.
func wrapApprovalPaths(paths []string, width int, style lipgloss.Style) [][]string {
	wrapped := make([][]string, 0, len(paths))
	for _, path := range paths {
		// Paths are model-supplied, so each is sanitized onto a single line
		// before it is wrapped to the available width.
		line := strings.TrimSpace(approvalPathReplacer.Replace(output.SanitizeDisplayText(path)))
		if line == "" {
			continue
		}
		wrapped = append(wrapped, wrapStyledApprovalPath(line, width, style))
	}
	return wrapped
}

// fitApprovalPaths reports how many leading paths fit limit lines and how many
// lines they use. A path counts only when all of its lines fit.
func fitApprovalPaths(wrapped [][]string, limit int) (shown, used int) {
	for _, pathLines := range wrapped {
		if used+len(pathLines) > limit {
			break
		}
		used += len(pathLines)
		shown++
	}
	return shown, used
}

// approvalRemainderLines wraps the remainder line for the paths that the budget
// leaves out, shrinking the shown paths until that line fits the same budget.
func approvalRemainderLines(wrapped [][]string, shown, used, count, width int, style lipgloss.Style) (int, int, []string) {
	var lines []string
	for {
		remaining := count - shown
		if remaining <= 0 {
			return shown, used, nil
		}
		lines = wrapStyledApprovalPath(fmt.Sprintf("and %d more outside the project", remaining), width, style)
		if shown == 0 || used+len(lines) <= maxApprovalOutsidePathLines {
			return shown, used, lines
		}
		shown--
		used -= len(wrapped[shown])
	}
}

// wrapStyledApprovalPath wraps one path line to width and splits the styled
// result back into lines, mirroring wrapStyledDelegationLines.
func wrapStyledApprovalPath(line string, width int, style lipgloss.Style) []string {
	wrapped := style.Width(width).Render(line)
	return strings.Split(strings.TrimRight(wrapped, "\n"), "\n")
}
