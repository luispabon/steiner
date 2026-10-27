package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

const bashArtifactNoticeText = "warning: bash artifact truncated: partial output saved to .steiner/artifacts/call-1.txt"

func TestBashArtifactTruncatedAppendsWarningNotice(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	b.AppendEvent(output.NewBashArtifactTruncatedEvent(3, "call-1", ".steiner/artifacts/call-1.txt", bashArtifactNoticeText))

	if len(b.segments) != 1 {
		t.Fatalf("segments = %d, want 1: %#v", len(b.segments), b.segments)
	}
	seg := b.segments[0]
	if seg.kind != segmentNotice || seg.noticeSeverity != noticeWarn || seg.text != bashArtifactNoticeText || seg.noticeScope != "" {
		t.Fatalf("segment = %#v, want parent warning notice with message", seg)
	}
}

func TestBashArtifactTruncatedNoticeKeepsChildScope(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	event := output.NewBashArtifactTruncatedEvent(3, "call-1", ".steiner/artifacts/call-1.txt", bashArtifactNoticeText)
	event.Scope = output.EventScope{AgentID: "child-1", AgentType: "code"}
	b.AppendEvent(event)

	if len(b.segments) != 1 {
		t.Fatalf("segments = %d, want 1: %#v", len(b.segments), b.segments)
	}
	seg := b.segments[0]
	if seg.kind != segmentNotice || seg.noticeSeverity != noticeWarn || seg.text != bashArtifactNoticeText || seg.noticeScope != "child-1" {
		t.Fatalf("segment = %#v, want child warning notice scoped to child-1", seg)
	}
}

func TestBashArtifactTruncatedNoticesDoNotCollapseAcrossPaths(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	b.AppendEvent(output.NewBashArtifactTruncatedEvent(3, "call-1", ".steiner/artifacts/call-1.txt", bashArtifactNoticeText))
	b.AppendEvent(output.NewBashArtifactTruncatedEvent(3, "call-2", ".steiner/artifacts/call-2.txt", "warning: bash artifact truncated: partial output saved to .steiner/artifacts/call-2.txt"))

	if len(b.segments) != 2 {
		t.Fatalf("segments = %d, want 2: %#v", len(b.segments), b.segments)
	}
	for i, seg := range b.segments {
		if seg.noticeCount != 1 {
			t.Errorf("segment %d noticeCount = %d, want 1", i, seg.noticeCount)
		}
	}
}

func TestBashArtifactTruncatedEmptyMessageAppendsNothing(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	b.AppendEvent(output.NewBashArtifactTruncatedEvent(3, "call-1", "", ""))

	if len(b.segments) != 0 {
		t.Fatalf("segments = %#v, want none for empty message", b.segments)
	}
}
