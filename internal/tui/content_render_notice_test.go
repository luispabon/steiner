package tui

import (
	"testing"
)

func TestRenderNoticeSegment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		seg  contentSegment
		want string
	}{
		{name: "error glyph", seg: contentSegment{kind: segmentNotice, noticeSeverity: noticeError, text: "boom", noticeCount: 1}, want: "✗ boom\n"},
		{name: "warn glyph", seg: contentSegment{kind: segmentNotice, noticeSeverity: noticeWarn, text: "careful", noticeCount: 1}, want: "⚠ careful\n"},
		{name: "info glyph", seg: contentSegment{kind: segmentNotice, noticeSeverity: noticeInfo, text: "note", noticeCount: 1}, want: "○ note\n"},
		{name: "collapsed count suffix", seg: contentSegment{kind: segmentNotice, noticeSeverity: noticeError, text: "sub_agent rejected: x", noticeCount: 6}, want: "✗ sub_agent rejected: x ×6\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := newTestBuffer(t)
			if got := stripANSI(b.renderNoticeSegment(tt.seg)); got != tt.want {
				t.Fatalf("renderNoticeSegment() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestJoinSeparatorNoticeRuns(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		prev, next contentSegmentKind
		want       string
	}{
		{name: "notice after notice", prev: segmentNotice, next: segmentNotice, want: "\n"},
		{name: "notice after assistant", prev: segmentAssistantMarkdown, next: segmentNotice, want: "\n\n"},
		{name: "status after notice", prev: segmentNotice, next: segmentStatus, want: "\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := joinSeparator(tt.prev, tt.next); got != tt.want {
				t.Fatalf("joinSeparator() = %q, want %q", got, tt.want)
			}
		})
	}
}
