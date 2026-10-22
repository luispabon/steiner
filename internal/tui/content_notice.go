package tui

import "strings"

// noticeSeverity selects the glyph and colour of a transcript notice.
type noticeSeverity int

const (
	noticeInfo noticeSeverity = iota
	noticeWarn
	noticeError
)

// noticeDisplayMaxRunes caps the notice text shown in the transcript, counting
// the trailing ellipsis. The full text stays in the segment for collapse checks.
const noticeDisplayMaxRunes = 200

// AppendNotice appends a severity notice to the transcript. A notice whose
// severity, full text and scope match the last segment collapses into it and
// increments its count, so repeated identical failures render once with ×N.
func (b *contentBuffer) AppendNotice(sev noticeSeverity, text, scope string) {
	b.finishStreaming()
	if last := len(b.segments) - 1; last >= 0 {
		seg := &b.segments[last]
		if seg.kind == segmentNotice && seg.noticeSeverity == sev && seg.noticeKey == text && seg.noticeScope == scope {
			seg.noticeCount++
			seg.renderDirty = true
			b.invalidatePrefixIfCached(last)
			return
		}
	}
	b.segments = append(b.segments, contentSegment{
		kind:           segmentNotice,
		text:           noticeDisplayText(text),
		noticeSeverity: sev,
		noticeCount:    1,
		noticeScope:    scope,
		noticeKey:      text,
		renderDirty:    true,
	})
}

// noticeDisplayText returns the first line of text, trimmed and capped at
// noticeDisplayMaxRunes runes.
func noticeDisplayText(text string) string {
	first, _, _ := strings.Cut(text, "\n")
	runes := []rune(strings.TrimSpace(first))
	if len(runes) <= noticeDisplayMaxRunes {
		return string(runes)
	}
	return string(runes[:noticeDisplayMaxRunes-1]) + "…"
}
