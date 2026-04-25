package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestAppendEventDelegationStarted(t *testing.T) {
	event := output.NewDelegationStartedEvent("child-1", "fix the bug in module X")

	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	buffer.AppendEvent(event)

	if len(buffer.segments) != 1 {
		t.Errorf("segments count = %d, want 1", len(buffer.segments))
		return
	}

	seg := buffer.segments[0]
	if seg.kind != segmentPlain {
		t.Errorf("segment kind = %v, want segmentPlain", seg.kind)
	}

	// Verify no task content leakage
	if strings.Contains(seg.text, "module X") {
		t.Errorf("segment contains task content: %q", seg.text)
	}

	if !strings.Contains(seg.text, "delegate:") {
		t.Errorf("segment missing 'delegate:' prefix: %q", seg.text)
	}

	if !strings.Contains(seg.text, "child-1") {
		t.Errorf("segment missing agent ID: %q", seg.text)
	}
}

func TestAppendEventDelegationComplete(t *testing.T) {
	event := output.NewDelegationCompleteEvent("child-2", "complete", 5, 2000)

	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	buffer.AppendEvent(event)

	if len(buffer.segments) != 1 {
		t.Errorf("segments count = %d, want 1", len(buffer.segments))
		return
	}

	seg := buffer.segments[0]
	if seg.kind != segmentPlain {
		t.Errorf("segment kind = %v, want segmentPlain", seg.kind)
	}

	if !strings.Contains(seg.text, "delegate:") {
		t.Errorf("segment missing 'delegate:' prefix: %q", seg.text)
	}

	if !strings.Contains(seg.text, "complete") {
		t.Errorf("segment missing 'complete': %q", seg.text)
	}

	if !strings.Contains(seg.text, "child-2") {
		t.Errorf("segment missing agent ID: %q", seg.text)
	}

	if !strings.Contains(seg.text, "5 turns") {
		t.Errorf("segment missing turn count: %q", seg.text)
	}
}

func TestAppendEventDelegationFailed(t *testing.T) {
	event := output.NewDelegationFailedEvent("child-3", "build package", "compilation error")

	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	buffer.AppendEvent(event)

	if len(buffer.segments) != 1 {
		t.Errorf("segments count = %d, want 1", len(buffer.segments))
		return
	}

	seg := buffer.segments[0]
	if seg.kind != segmentPlain {
		t.Errorf("segment kind = %v, want segmentPlain", seg.kind)
	}

	if !strings.Contains(seg.text, "delegate:") {
		t.Errorf("segment missing 'delegate:' prefix: %q", seg.text)
	}

	if !strings.Contains(seg.text, "failed") {
		t.Errorf("segment missing 'failed': %q", seg.text)
	}

	if !strings.Contains(seg.text, "child-3") {
		t.Errorf("segment missing agent ID: %q", seg.text)
	}

	// Verify no task content or error details leak
	if strings.Contains(seg.text, "build package") {
		t.Errorf("segment contains task preview: %q", seg.text)
	}

	if strings.Contains(seg.text, "compilation error") {
		t.Errorf("segment contains error message: %q", seg.text)
	}
}

func TestAppendEventDelegationNoContentLeakage(t *testing.T) {
	tests := []struct {
		name  string
		event output.Event
	}{
		{
			name:  "delegation_started",
			event: output.NewDelegationStartedEvent("agent-1", "secret task content here"),
		},
		{
			name:  "delegation_complete",
			event: output.NewDelegationCompleteEvent("agent-2", "complete", 1, 100),
		},
		{
			name:  "delegation_failed",
			event: output.NewDelegationFailedEvent("agent-3", "secret task", "error details"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buffer := &contentBuffer{
				segments: make([]contentSegment, 0),
			}

			buffer.AppendEvent(tt.event)

			if len(buffer.segments) != 1 {
				t.Errorf("segments count = %d, want 1", len(buffer.segments))
				return
			}

			seg := buffer.segments[0]

			// These secrets should never appear in rendered output
			secrets := []string{
				"secret task content here",
				"error details",
				"secret task",
			}

			for _, secret := range secrets {
				if strings.Contains(seg.text, secret) {
					t.Errorf("segment contains sensitive content: %q found in %q", secret, seg.text)
				}
			}
		})
	}
}

func TestFormatDelegationEvent(t *testing.T) {
	tests := []struct {
		name      string
		event     output.Event
		wantMatch string
	}{
		{
			name:      "started",
			event:     output.NewDelegationStartedEvent("test-agent", "do work"),
			wantMatch: "delegate: starting test-agent",
		},
		{
			name:      "complete",
			event:     output.NewDelegationCompleteEvent("test-agent", "complete", 2, 500),
			wantMatch: "delegate: complete test-agent (2 turns)",
		},
		{
			name:      "failed",
			event:     output.NewDelegationFailedEvent("test-agent", "task", "err"),
			wantMatch: "delegate: failed test-agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDelegationEvent(tt.event)
			if result != tt.wantMatch {
				t.Errorf("formatDelegationEvent = %q, want %q", result, tt.wantMatch)
			}
		})
	}
}

func TestAppendEventContextDiagnosticsAreVisible(t *testing.T) {
	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	buffer.AppendEvent(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:            "compaction",
		Severity:        "warning",
		SessionState:    "fragile",
		CompactionCount: 2,
		RestartGuidance: "restart soon in a fresh session; repeated compaction is making retention fragile",
	}))
	buffer.AppendEvent(output.NewContextSessionHealthEvent("conversation", 2, 2, "warning", "fragile", "restart soon in a fresh session; repeated compaction is making retention fragile"))

	if len(buffer.segments) != 2 {
		t.Fatalf("segments count = %d, want 2", len(buffer.segments))
	}
	// First segment should be compaction banner
	if buffer.segments[0].kind != segmentCompactionBanner {
		t.Fatalf("segment[0] kind = %v, want segmentCompactionBanner", buffer.segments[0].kind)
	}
	if buffer.segments[0].compactionData == nil {
		t.Fatalf("segment[0] compactionData is nil")
	}
	if !strings.Contains(buffer.segments[0].compactionData.summary, "compacted") {
		t.Fatalf("compaction summary = %q, want visible compaction data", buffer.segments[0].compactionData.summary)
	}
	// Second segment should be session health (still thinking)
	if buffer.segments[1].kind != segmentThinking {
		t.Fatalf("segment[1] kind = %v, want segmentThinking", buffer.segments[1].kind)
	}
	if got := buffer.segments[1].text; !strings.Contains(got, "session health") {
		t.Fatalf("session health text = %q, want visible health state", got)
	}
}

func TestAppendEventContextReportRendersMarkdownBlock(t *testing.T) {
	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	buffer.AppendEvent(output.NewContextReportEvent("# Last Request Context\n\nPrompt tokens: `42`"))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	if got := buffer.segments[0].text; !strings.Contains(got, "Last Request Context") || !strings.Contains(got, "Prompt tokens") {
		t.Fatalf("segment text = %q, want context report block", got)
	}
}

func TestPluralTurns(t *testing.T) {
	tests := []struct {
		count int
		want  string
	}{
		{0, "0 turns"},
		{1, "1 turn"},
		{2, "2 turns"},
		{10, "10 turns"},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := pluralTurns(tt.count)
			if result != tt.want {
				t.Errorf("pluralTurns(%d) = %q, want %q", tt.count, result, tt.want)
			}
		})
	}
}
