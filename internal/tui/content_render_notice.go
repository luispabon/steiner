package tui

import "fmt"

// renderNoticeSegment renders a notice as one line: a severity glyph, the
// display text in the base foreground, and a muted ×N suffix when the notice
// collapsed more than one occurrence. Notices have no border.
func (b *contentBuffer) renderNoticeSegment(segment contentSegment) string {
	glyph := b.styles.FgMute.Render("○")
	switch segment.noticeSeverity {
	case noticeError:
		glyph = b.styles.ErrorStyle.Render("✗")
	case noticeWarn:
		glyph = b.styles.WarningStyle.Render("⚠")
	}
	line := glyph + " " + b.baseTextStyle().Render(segment.text)
	if segment.noticeCount > 1 {
		line += " " + b.styles.FgMute.Render(fmt.Sprintf("×%d", segment.noticeCount))
	}
	return line + "\n"
}
