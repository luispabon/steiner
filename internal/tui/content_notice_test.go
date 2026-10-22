package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

type noticeCall struct {
	sev   noticeSeverity
	text  string
	scope string
}

func TestAppendNoticeCollapsesOnlyAdjacentMatches(t *testing.T) {
	t.Parallel()
	admission := noticeCall{noticeError, "sub_agent rejected: quota exceeded", "agent-1"}
	tests := []struct {
		name       string
		calls      []noticeCall
		wantCounts []int
	}{
		{name: "six identical admissions collapse", calls: slices.Repeat([]noticeCall{admission}, 6), wantCounts: []int{6}},
		{name: "different scope does not collapse", calls: []noticeCall{admission, {noticeError, admission.text, "agent-2"}}, wantCounts: []int{1, 1}},
		{name: "different severity does not collapse", calls: []noticeCall{admission, {noticeWarn, admission.text, admission.scope}}, wantCounts: []int{1, 1}},
		{name: "same first line with different later text does not collapse", calls: []noticeCall{{noticeError, "boom\ndetail a", ""}, {noticeError, "boom\ndetail b", ""}}, wantCounts: []int{1, 1}},
		{name: "non-adjacent repeat does not collapse", calls: []noticeCall{admission, {noticeInfo, "other", ""}, admission}, wantCounts: []int{1, 1, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := newTestBuffer(t)
			for _, c := range tt.calls {
				b.AppendNotice(c.sev, c.text, c.scope)
			}
			var got []int
			for _, seg := range b.segments {
				if seg.kind != segmentNotice {
					t.Fatalf("segment kind = %v, want segmentNotice", seg.kind)
				}
				got = append(got, seg.noticeCount)
			}
			if !slices.Equal(got, tt.wantCounts) {
				t.Fatalf("notice counts = %v, want %v", got, tt.wantCounts)
			}
		})
	}
}

func TestNoticeDisplayTextCapsFirstLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "plain", text: "boom", want: "boom"},
		{name: "trims spaces", text: "  boom  ", want: "boom"},
		{name: "first line only", text: "boom\ndetail", want: "boom"},
		{name: "caps at 200 runes including ellipsis", text: strings.Repeat("é", 250), want: strings.Repeat("é", 199) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := noticeDisplayText(tt.text); got != tt.want {
				t.Fatalf("noticeDisplayText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAppendNoticeKeepsFullTextForCollapse(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	long := strings.Repeat("a", 250)
	b.AppendNotice(noticeError, long, "")
	b.AppendNotice(noticeError, long, "")
	if len(b.segments) != 1 || b.segments[0].noticeKey != long || b.segments[0].noticeCount != 2 {
		t.Fatalf("repeated long notice = %#v, want one segment with full key and count 2", b.segments)
	}
	b.AppendNotice(noticeError, long+"b", "")
	if len(b.segments) != 2 {
		t.Fatalf("notice differing only past the display cap collapsed: %#v", b.segments)
	}
}

func TestAdmissionRejectionNoticeCollapsesPerScope(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	reject := func(agentID string) {
		b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "call", "", errors.New("quota exceeded"), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected", AgentID: agentID}))
	}
	for range 6 {
		reject("agent-1")
	}
	reject("agent-2")
	if len(b.segments) != 2 || b.segments[0].text != "sub_agent rejected: quota exceeded" || b.segments[0].noticeCount != 6 || b.segments[1].noticeCount != 1 {
		t.Fatalf("admission notices = %#v, want six collapsed for agent-1 and one for agent-2", b.segments)
	}
}

func TestErrorSourcesRenderAsErrorNotices(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	b.appendStopReasonEvent(output.NewStopReasonEvent(1, "error", errors.New("boom")))
	if len(b.segments) != 1 || b.segments[0].kind != segmentNotice || b.segments[0].noticeSeverity != noticeError || b.segments[0].text != "boom" {
		t.Fatalf("stop reason error segment = %#v, want error notice boom", b.segments)
	}

	styles := testStyles("#5599ff")
	m := &Model{content: contentBuffer{styles: styles}, styles: styles}
	m.appendError(errors.New("status: provider failed"))
	if got := m.content.segments; len(got) != 1 || got[0].kind != segmentNotice || got[0].noticeSeverity != noticeError || got[0].text != "provider failed" {
		t.Fatalf("appendError segment = %#v, want error notice without status prefix", got)
	}
}
