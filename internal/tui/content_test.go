package tui

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
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
	if seg.kind != segmentDelegation {
		t.Errorf("segment kind = %v, want segmentDelegation", seg.kind)
	}

	if seg.delegData == nil {
		t.Fatal("delegData = nil, want delegation state")
	}

	// Verify no task content leakage
	if strings.Contains(seg.text, "module X") {
		t.Errorf("segment text contains task content: %q", seg.text)
	}

	if seg.delegData.agentID != "child-1" {
		t.Errorf("delegData.agentID = %q, want %q", seg.delegData.agentID, "child-1")
	}

	if seg.delegData.status != "active" {
		t.Errorf("delegData.status = %q, want %q", seg.delegData.status, "active")
	}
}

func TestAppendEventDelegationComplete(t *testing.T) {
	event := output.NewDelegationCompleteEvent("child-2", "complete", 5, 2000, "")

	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	buffer.AppendEvent(event)

	if len(buffer.segments) != 1 {
		t.Errorf("segments count = %d, want 1", len(buffer.segments))
		return
	}

	seg := buffer.segments[0]
	if seg.kind != segmentDelegation {
		t.Errorf("segment kind = %v, want segmentDelegation", seg.kind)
	}

	if seg.delegData == nil {
		t.Fatal("delegData = nil, want delegation state")
	}

	if seg.delegData.agentID != "child-2" {
		t.Errorf("delegData.agentID = %q, want %q", seg.delegData.agentID, "child-2")
	}

	if seg.delegData.status != "complete" {
		t.Errorf("delegData.status = %q, want %q", seg.delegData.status, "complete")
	}

	if seg.delegData.turnCount != 5 {
		t.Errorf("delegData.turnCount = %d, want 5", seg.delegData.turnCount)
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
	if seg.kind != segmentDelegation {
		t.Errorf("segment kind = %v, want segmentDelegation", seg.kind)
	}

	if seg.delegData == nil {
		t.Fatal("delegData = nil, want delegation state")
	}

	if seg.delegData.agentID != "child-3" {
		t.Errorf("delegData.agentID = %q, want %q", seg.delegData.agentID, "child-3")
	}

	if seg.delegData.status != "failed" {
		t.Errorf("delegData.status = %q, want %q", seg.delegData.status, "failed")
	}

	// Verify no task content or error details leak
	if strings.Contains(seg.text, "build package") {
		t.Errorf("segment text contains task preview: %q", seg.text)
	}

	if strings.Contains(seg.text, "compilation error") {
		t.Errorf("segment text contains error message: %q", seg.text)
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
			event: output.NewDelegationCompleteEvent("agent-2", "complete", 1, 100, ""),
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

			// These secrets should never appear in the segment text or delegData fields.
			secrets := []string{
				"secret task content here",
				"error details",
				"secret task",
			}

			for _, secret := range secrets {
				if strings.Contains(seg.text, secret) {
					t.Errorf("segment.text contains sensitive content: %q found in %q", secret, seg.text)
				}
				if seg.delegData != nil {
					if strings.Contains(seg.delegData.errMsg, secret) {
						t.Errorf("delegData.errMsg contains sensitive content: %q", secret)
					}
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
			event:     output.NewDelegationCompleteEvent("test-agent", "complete", 2, 500, ""),
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

func TestDelegationSpinnerAdvancement(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("spin-agent", "task"))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}

	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil")
	}
	if dd.spinnerFrame != 0 {
		t.Errorf("initial spinnerFrame = %d, want 0", dd.spinnerFrame)
	}

	buffer.AdvanceDelegationSpinners()
	if dd.spinnerFrame != 1 {
		t.Errorf("spinnerFrame after 1 advance = %d, want 1", dd.spinnerFrame)
	}

	// Advance through all frames and confirm wrap-around.
	for i := 0; i < len(spinnerFrames)+1; i++ {
		buffer.AdvanceDelegationSpinners()
	}
	// Frame should be within bounds.
	if dd.spinnerFrame < 0 || dd.spinnerFrame >= len(spinnerFrames) {
		t.Errorf("spinnerFrame out of range: %d", dd.spinnerFrame)
	}
}

func TestDelegationElapsedTimeDisplay(t *testing.T) {
	tests := []struct {
		name      string
		startNano int64
		endNano   int64
		want      string
	}{
		{"milliseconds", 0, 500_000_000, "500ms"},
		{"seconds", 0, 5_000_000_000, "5s"},
		{"minutes", 0, 90_000_000_000, "1m30s"},
		{"zero", 0, 0, "0ms"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatElapsed(tt.startNano, tt.endNano)
			if got != tt.want {
				t.Errorf("formatElapsed(%d, %d) = %q, want %q", tt.startNano, tt.endNano, got, tt.want)
			}
		})
	}
}

func TestDelegationLifecycle(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	// Start delegation.
	buffer.AppendEvent(output.NewDelegationStartedEvent("life-agent", "do work"))
	if !buffer.HasActiveDelegations() {
		t.Fatal("HasActiveDelegations = false after started, want true")
	}

	// Complete delegation.
	buffer.AppendEvent(output.NewDelegationCompleteEvent("life-agent", "done", 3, 600, ""))
	if buffer.HasActiveDelegations() {
		t.Fatal("HasActiveDelegations = true after complete, want false")
	}

	// Segment should be updated in place (still 1 segment).
	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil after complete")
	}
	if dd.status != "complete" {
		t.Errorf("status = %q, want complete", dd.status)
	}
	if dd.turnCount != 3 {
		t.Errorf("turnCount = %d, want 3", dd.turnCount)
	}
}

func TestDelegationFailedLifecycle(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("fail-agent", "risky task"))
	buffer.AppendEvent(output.NewDelegationFailedEvent("fail-agent", "risky task", "boom"))

	if buffer.HasActiveDelegations() {
		t.Fatal("HasActiveDelegations = true after failed, want false")
	}

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil after failed")
	}
	if dd.status != "failed" {
		t.Errorf("status = %q, want failed", dd.status)
	}
	// Error message must not leak.
	if dd.errMsg == "boom" {
		t.Error("errMsg must not store raw error details")
	}
}

func TestDelegationToggleOutput(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewDelegationCompleteEvent("toggle-agent", "done", 1, 50, "result text"))

	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil")
	}
	// Default: collapsed.
	if !dd.collapsed {
		t.Error("delegation should be collapsed by default")
	}

	// Toggle once → expanded; output text should appear.
	buffer.ToggleLastDelegationOutput()
	if dd.collapsed {
		t.Error("delegation should be expanded after first toggle")
	}
	rendered := buffer.String(80)
	if !strings.Contains(rendered, "result text") {
		t.Errorf("rendered output missing 'result text' in expanded state: %q", rendered)
	}

	// Toggle again → collapsed; output text hidden, hint shown.
	buffer.ToggleLastDelegationOutput()
	if !dd.collapsed {
		t.Error("delegation should be collapsed after second toggle")
	}
	rendered = buffer.String(80)
	if strings.Contains(rendered, "result text") {
		t.Errorf("rendered output should not contain 'result text' in collapsed state: %q", rendered)
	}
	if !strings.Contains(rendered, "[output hidden") {
		t.Errorf("rendered output missing '[output hidden' hint in collapsed state: %q", rendered)
	}
}

func TestDelegationExpandedOutputIsNotTruncated(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
	}
	longOutput := strings.Repeat("x", 650) + "tail marker"

	buffer.AppendEvent(output.NewDelegationCompleteEvent("long-agent", "done", 1, 50, longOutput))
	buffer.ToggleLastDelegationOutput()

	rendered := buffer.String(80)
	if !strings.Contains(rendered, "tail marker") {
		t.Fatalf("expanded delegation output was truncated: %q", rendered)
	}
}

func TestDelegationBlockRendering(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*contentBuffer)
		checks  []string
		nocheck []string
	}{
		{
			name: "active_has_agent_id",
			setup: func(b *contentBuffer) {
				b.AppendEvent(output.NewDelegationStartedEvent("render-agent", "preview text"))
			},
			checks: []string{"render-agent", "preview text"},
		},
		{
			name: "complete_has_turns",
			setup: func(b *contentBuffer) {
				b.AppendEvent(output.NewDelegationCompleteEvent("c-agent", "done", 7, 300, ""))
			},
			checks: []string{"c-agent", "7 turns"},
		},
		{
			name: "failed_has_agent_id",
			setup: func(b *contentBuffer) {
				b.AppendEvent(output.NewDelegationStartedEvent("f-agent", "work"))
				b.AppendEvent(output.NewDelegationFailedEvent("f-agent", "work", "err"))
			},
			checks:  []string{"f-agent"},
			nocheck: []string{"err"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buffer := &contentBuffer{
				segments:      make([]contentSegment, 0),
				collapseState: make(map[int]bool),
				styles:        theme.BuildStyles(theme.AccentAmber),
			}
			tt.setup(buffer)
			rendered := buffer.String(80)
			for _, want := range tt.checks {
				if !strings.Contains(rendered, want) {
					t.Errorf("rendered %q missing %q", rendered, want)
				}
			}
			for _, nope := range tt.nocheck {
				if strings.Contains(rendered, nope) {
					t.Errorf("rendered %q should not contain %q", rendered, nope)
				}
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

func TestAppendEventSuppressesScaffoldInferenceChunksWhenDebugDisabled(t *testing.T) {
	buffer := &contentBuffer{
		segments:                      make([]contentSegment, 0),
		collapseState:                 make(map[int]bool),
		showInternalScaffoldInference: false,
	}

	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "internal reasoning", output.ChunkSourceScaffoldInference))
	buffer.AppendEvent(output.NewAssistantChunkEventWithSource(1, `{"intent":"inspect","next":"read file"}`, output.ChunkSourceScaffoldInference))
	buffer.AppendEvent(output.NewAssistantChunkEvent(1, "visible answer"))
	buffer.finishStreaming()

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	if got := buffer.segments[0].text; !strings.Contains(got, "visible answer") {
		t.Fatalf("segment text = %q, want visible answer", got)
	}
	if got := buffer.String(80); strings.Contains(got, "internal reasoning") || strings.Contains(got, `"intent":"inspect"`) {
		t.Fatalf("rendered content leaked scaffold inference: %q", got)
	}
}

func TestAppendEventShowsScaffoldInferenceChunksWhenDebugEnabled(t *testing.T) {
	buffer := &contentBuffer{
		segments:                      make([]contentSegment, 0),
		collapseState:                 make(map[int]bool),
		showInternalScaffoldInference: true,
	}

	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "internal reasoning", output.ChunkSourceScaffoldInference))
	buffer.AppendEvent(output.NewAssistantChunkEventWithSource(1, `{"intent":"inspect","next":"read file"}`, output.ChunkSourceScaffoldInference))
	buffer.finishStreaming()

	if len(buffer.segments) != 2 {
		t.Fatalf("segments count = %d, want 2", len(buffer.segments))
	}
	if buffer.segments[0].kind != segmentThinkingBlock {
		t.Fatalf("segment[0].kind = %v, want segmentThinkingBlock", buffer.segments[0].kind)
	}
	if got := buffer.segments[1].text; !strings.Contains(got, `"intent":"inspect"`) {
		t.Fatalf("assistant segment = %q, want scaffold json", got)
	}
}

func TestThinkingBlocksStartExpandedWhileStreamingAndCollapseWhenFinished(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
		showThinking:  true,
	}

	buffer.AppendEvent(output.NewThinkingChunkEvent(1, "first line"))
	buffer.AppendEvent(output.NewThinkingChunkEvent(1, "\nsecond line"))

	if got := len(buffer.segments); got != 1 {
		t.Fatalf("segments count while streaming = %d, want 1", got)
	}
	thinking := buffer.segments[0].thinkData
	if thinking == nil {
		t.Fatal("thinkData = nil, want thinking block")
	}
	if thinking.collapsed {
		t.Fatal("thinking block collapsed while streaming, want expanded")
	}
	if !thinking.streaming {
		t.Fatal("thinking block streaming = false, want true")
	}
	renderedStreaming := buffer.String(80)
	for _, want := range []string{"▾ Thinking", "first line", "second line"} {
		if !strings.Contains(renderedStreaming, want) {
			t.Fatalf("streaming render = %q, want %q", renderedStreaming, want)
		}
	}

	buffer.finalizeThinkingBlock()

	if thinking.streaming {
		t.Fatal("thinking block streaming = true after finalize, want false")
	}
	if !thinking.collapsed {
		t.Fatal("thinking block collapsed = false after finalize, want true")
	}
	if got := buffer.collapseState[0]; !got {
		t.Fatalf("collapseState[0] = %v, want true after finalize", got)
	}
	renderedCollapsed := buffer.String(80)
	if !strings.Contains(renderedCollapsed, "▸ Thinking · first line") {
		t.Fatalf("collapsed render = %q, want collapsed summary", renderedCollapsed)
	}
}

func TestAPIResponseFinalizesScaffoldInferenceJSONAfterThinking(t *testing.T) {
	buffer := &contentBuffer{
		segments:                      make([]contentSegment, 0),
		collapseState:                 make(map[int]bool),
		styles:                        theme.BuildStyles(theme.AccentAmber),
		showThinking:                  true,
		showInternalScaffoldInference: true,
	}

	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "internal reasoning", output.ChunkSourceScaffoldInference))
	buffer.AppendEvent(output.NewAssistantChunkEventWithSource(1, `{"intent":"inspect","next":"read file"}`, output.ChunkSourceScaffoldInference))
	buffer.AppendEvent(output.NewAPIResponseEvent(nil, nil, "stop", nil))

	if got := strings.TrimSpace(buffer.streamBuffer); got != "" {
		t.Fatalf("streamBuffer = %q, want empty after API response", got)
	}
	if got := len(buffer.segments); got != 2 {
		t.Fatalf("segments count = %d, want 2", got)
	}
	if buffer.segments[0].thinkData == nil || !buffer.segments[0].thinkData.collapsed {
		t.Fatal("thinking block not collapsed after API response finalization")
	}
	if got := buffer.segments[1].text; !strings.Contains(got, `"intent":"inspect"`) || !strings.Contains(got, `"next":"read file"`) {
		t.Fatalf("assistant segment = %q, want finalized scaffold json", got)
	}
}

func TestStreamingScaffoldInferencePreviewHardWrapsLongJSON(t *testing.T) {
	buffer := &contentBuffer{
		showInternalScaffoldInference: true,
		styles:                        theme.BuildStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewAssistantChunkEventWithSource(1, `{"intent":"inspect_scratchpad_go_to_find_scaffold_inference","next":"read_turn_progression_go"}`, output.ChunkSourceScaffoldInference))

	rendered := buffer.String(30)
	if !strings.Contains(rendered, "\n") {
		t.Fatalf("rendered = %q, want wrapped preview", rendered)
	}
	for _, want := range []string{`"intent":"inspect_`, `"next":"read_turn_progression`, `_go"`} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want %q", rendered, want)
		}
	}
}

func TestAppendUserMarkdownSegmentKind(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantKind contentSegmentKind
	}{
		{
			name:     "plain text is also segmentUserMarkdown",
			text:     "Just a normal question",
			wantKind: segmentUserMarkdown,
		},
		{
			name:     "markdown heading becomes segmentUserMarkdown",
			text:     "# Heading\nsome content",
			wantKind: segmentUserMarkdown,
		},
		{
			name:     "fenced code becomes segmentUserMarkdown",
			text:     "Check this:\n```go\nfmt.Println()\n```",
			wantKind: segmentUserMarkdown,
		},
		{
			name:     "bulleted list becomes segmentUserMarkdown",
			text:     "Steps:\n- one\n- two",
			wantKind: segmentUserMarkdown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &contentBuffer{
				segments:      make([]contentSegment, 0),
				collapseState: make(map[int]bool),
			}
			b.AppendUser(tt.text)
			if len(b.segments) != 1 {
				t.Fatalf("segments count = %d, want 1", len(b.segments))
			}
			if got := b.segments[0].kind; got != tt.wantKind {
				t.Errorf("segment kind = %v, want %v", got, tt.wantKind)
			}
		})
	}
}

func TestRenderUserMarkdownSegmentContainsText(t *testing.T) {
	b := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}
	b.segments = []contentSegment{
		{kind: segmentUserMarkdown, text: "# Title\n\nSome bold content."},
	}

	got := b.String(80)
	// ANSI escape codes may split words; check individual tokens.
	for _, want := range []string{"Title", "bold", "content", "┃"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered user markdown missing %q", want)
		}
	}
}

func TestRenderPlainUserSegmentUnchanged(t *testing.T) {
	b := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}
	b.segments = []contentSegment{
		{kind: segmentUser, text: "just a plain message"},
	}

	got := b.String(80)
	if !strings.Contains(got, "just a plain message") {
		t.Errorf("rendered plain user %q missing text", got)
	}
	if !strings.Contains(got, "┃") {
		t.Errorf("rendered plain user %q missing user bar character", got)
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

func TestAppendEventToolPreviewUsesStructuredData(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	before := false
	buffer.AppendEvent(output.NewToolCallStartedEventWithPreviewState(1, "write", "call_1", map[string]any{
		"path":    "notes.md",
		"content": "hello\nworld\n",
	}, &before))
	buffer.AppendEvent(output.NewToolCallFinishedEventWithPreview(1, "write", "call_1", `{"path":"notes.md","bytes_written":12}`, nil, output.ToolPreview{
		Kind:     output.ToolPreviewKindFileWrite,
		Path:     "explicit-notes.md",
		Language: "markdown",
		Contents: "explicit\npreview\n",
		Created:  true,
	}))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}

	seg := buffer.segments[0].toolData
	if seg == nil {
		t.Fatalf("tool segment is nil")
	}
	if got, want := seg.bodyKind, "file"; got != want {
		t.Fatalf("bodyKind = %q, want %q", got, want)
	}
	if got, want := seg.preview.Kind, output.ToolPreviewKindFileWrite; got != want {
		t.Fatalf("preview kind = %q, want %q", got, want)
	}
	if seg.writeTargetExistedBefore == nil || *seg.writeTargetExistedBefore {
		t.Fatalf("writeTargetExistedBefore = %v, want false", seg.writeTargetExistedBefore)
	}
	if got, want := seg.preview.Path, "explicit-notes.md"; got != want {
		t.Fatalf("preview path = %q, want %q", got, want)
	}
	if got, want := seg.preview.Contents, "explicit\npreview\n"; got != want {
		t.Fatalf("preview contents = %q, want %q", got, want)
	}
	if !seg.preview.Created {
		t.Fatalf("preview created = false, want true")
	}
}

func TestAppendEventDisplayFileUsesExplicitPreviewDocument(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	preview := output.FormatFilePreviewWithLimit("snippet.go", `package main
func main() {}
`, 10)
	buffer.AppendEvent(output.NewDisplayFileEvent(output.DisplayFilePayload{
		Path:    "snippet.go",
		Preview: preview,
	}))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	seg := buffer.segments[0].toolData
	if seg == nil {
		t.Fatal("tool segment is nil")
	}
	if !strings.EqualFold(seg.tool, "display_file") {
		t.Fatalf("tool = %q, want display_file", seg.tool)
	}
	if seg.collapsed {
		t.Fatal("display_file segment is collapsed, want expanded")
	}
	if seg.displayPreview == nil {
		t.Fatal("display preview is nil")
	}
	if got, want := seg.displayPreview.Path, "snippet.go"; got != want {
		t.Fatalf("display preview path = %q, want %q", got, want)
	}
	if got, want := seg.displayPreview.Kind, output.PreviewFormatKindFile; got != want {
		t.Fatalf("display preview kind = %q, want %q", got, want)
	}
}

func TestAppendEventDisplayFileSuppressesGenericToolLifecycleRows(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	preview := output.FormatFilePreviewWithLimit("snippet.go", `package main
func main() {}
`, 10)

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "display_file", "call_1", map[string]any{
		"path": "snippet.go",
	}))
	buffer.AppendEvent(output.NewDisplayFileEvent(output.DisplayFilePayload{
		Path:    "snippet.go",
		Preview: preview,
	}))
	buffer.AppendEvent(output.NewToolCallFinishedEvent(1, "display_file", "call_1", `{"path":"snippet.go","status":"displayed"}`, nil))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	seg := buffer.segments[0].toolData
	if seg == nil {
		t.Fatal("tool segment is nil")
	}
	if !strings.EqualFold(seg.tool, "display_file") {
		t.Fatalf("tool = %q, want display_file", seg.tool)
	}
	if seg.displayPreview == nil {
		t.Fatal("display preview is nil")
	}
	if got, want := seg.displayPreview.Path, "snippet.go"; got != want {
		t.Fatalf("display preview path = %q, want %q", got, want)
	}
	if got := seg.body; got != "" {
		t.Fatalf("body = %q, want empty body", got)
	}
}

func TestAppendEventBuildsFallbackPreviewFromRetainedArgs(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "edit", "call_1", map[string]any{
		"path":       "main.go",
		"old_string": "oldLine()",
		"new_string": "newLine()",
	}))
	buffer.AppendEvent(output.NewToolCallFinishedEvent(1, "edit", "call_1", `{"path":"main.go","replacements":1}`, nil))

	if len(buffer.segments) != 1 || buffer.segments[0].toolData == nil {
		t.Fatalf("tool segments = %#v, want one populated tool segment", buffer.segments)
	}
	seg := buffer.segments[0].toolData
	if got, want := seg.preview.Kind, output.ToolPreviewKindEditDiff; got != want {
		t.Fatalf("preview kind = %q, want %q", got, want)
	}
	if got, want := seg.bodyKind, "diff"; got != want {
		t.Fatalf("bodyKind = %q, want %q", got, want)
	}
	if got, want := seg.preview.Before, "oldLine()"; got != want {
		t.Fatalf("preview before = %q, want %q", got, want)
	}
	if got, want := seg.preview.After, "newLine()"; got != want {
		t.Fatalf("preview after = %q, want %q", got, want)
	}
}

func TestAppendEventScopedChildToolEventDoesNotAppendTopLevelToolSegment(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "do work"))
	buffer.segments[0].renderDirty = false

	buffer.AppendEvent(output.WithAgentScope(
		output.NewToolCallStartedEvent(1, "read", "call_1", map[string]any{"path": "README.md"}),
		"child-1",
	))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	if got := buffer.segments[0].kind; got != segmentDelegation {
		t.Fatalf("segment kind = %v, want segmentDelegation", got)
	}
	if !buffer.segments[0].renderDirty {
		t.Fatal("delegation segment renderDirty = false, want true")
	}
}

func TestAppendEventScopedChildToolEventFallsBackWhenTargetMissing(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.WithAgentScope(
		output.NewToolCallStartedEvent(1, "read", "call_1", map[string]any{"path": "README.md"}),
		"missing-child",
	))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	if got := buffer.segments[0].kind; got != segmentToolCall {
		t.Fatalf("segment kind = %v, want segmentToolCall", got)
	}
	if buffer.segments[0].toolData == nil {
		t.Fatal("toolData = nil, want tool segment")
	}
}

func TestAppendEventUnscopedParentToolBehaviorUnchanged(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "do work"))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "read", "call_1", map[string]any{"path": "README.md"}))

	if len(buffer.segments) != 2 {
		t.Fatalf("segments count = %d, want 2", len(buffer.segments))
	}
	if got := buffer.segments[0].kind; got != segmentDelegation {
		t.Fatalf("first segment kind = %v, want segmentDelegation", got)
	}
	if got := buffer.segments[1].kind; got != segmentToolCall {
		t.Fatalf("second segment kind = %v, want segmentToolCall", got)
	}
	if buffer.segments[1].toolData == nil {
		t.Fatal("toolData = nil, want tool segment")
	}
}

func TestRenderToolPreviewUsesStructuredFilePreview(t *testing.T) {
	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}
	buffer.segments = []contentSegment{
		{
			kind: segmentToolCall,
			toolData: &toolCallSegment{
				tool:      "write",
				args:      "notes.md",
				bodyKind:  "file",
				collapsed: false,
				preview: output.ToolPreview{
					Kind:     output.ToolPreviewKindFileWrite,
					Path:     "notes.md",
					Contents: "hello\nworld\n",
					Created:  true,
				},
			},
		},
	}

	got := buffer.String(80)
	for _, want := range []string{"new file preview", "notes.md", "hello", "world"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered preview %q missing %q", got, want)
		}
	}
}

func TestRenderToolPreviewUsesChromaStylesForMarkdown(t *testing.T) {
	useTrueColor(t)

	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}

	doc := output.FormatFilePreview("POEM.md", "# POEM\n")
	if len(doc.Lines) == 0 {
		t.Fatal("preview document has no lines")
	}

	got := buffer.renderPreviewLine(doc.Lines[0])
	plain := buffer.baseTextStyle().Render("# POEM")
	if got == plain {
		t.Fatalf("markdown heading rendered with plain style: %q", got)
	}
	if !strings.Contains(stripANSI(got), "# POEM") {
		t.Fatalf("rendered markdown heading %q missing text", got)
	}
}

func TestBuildPlainLinesRendersCurrentScratchpadFields(t *testing.T) {
	buffer := &contentBuffer{
		styles: theme.BuildStyles(theme.AccentAmber),
	}
	lines := buffer.buildPlainLines(&toolCallSegment{
		tool: "scratchpad",
		rawArgs: map[string]any{
			"intent":    "inspect note",
			"decisions": "keep it simple",
			"open":      "none",
			"next":      "read file",
		},
	})
	got := stripANSI(strings.Join(lines, "\n"))
	for _, want := range []string{"Intent: inspect note", "Decisions: keep it simple", "Open: none", "Next: read file"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered scratchpad args %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "goal:") || strings.Contains(got, "plan:") || strings.Contains(got, "step:") || strings.Contains(got, "files:") {
		t.Fatalf("rendered scratchpad args still contain legacy fields: %q", got)
	}
}

func TestRenderToolPreviewUsesChromaStylesForMakefile(t *testing.T) {
	useTrueColor(t)

	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}

	doc := output.FormatFilePreview("Makefile", "BIN_DIR := bin\nGO_FILES := *.go\n")
	if len(doc.Lines) == 0 {
		t.Fatal("preview document has no lines")
	}

	got := buffer.renderPreviewLine(doc.Lines[0])
	plain := buffer.baseTextStyle().Render("BIN_DIR := bin")
	if got == plain {
		t.Fatalf("makefile variable rendered with plain style: %q", got)
	}
	if !strings.Contains(stripANSI(got), "BIN_DIR") {
		t.Fatalf("rendered makefile line %q missing variable name", got)
	}
}

func TestRenderDisplayFilePreviewUsesCaptionAndHighlightedContent(t *testing.T) {
	preview := output.FormatFilePreviewWithLimit("snippet.go", `package main
func main() {}
`, 10)
	if len(preview.Lines) == 0 || !lineHasHighlightedSpan(preview.Lines[0]) {
		t.Fatal("preview document is not syntax-highlighted")
	}
	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}
	buffer.segments = []contentSegment{
		{
			kind: segmentToolCall,
			toolData: &toolCallSegment{
				tool:           "display_file",
				args:           "snippet.go",
				bodyKind:       "file",
				collapsed:      false,
				displayPreview: &preview,
			},
		},
	}

	got := buffer.String(100)
	for _, want := range []string{"▾", "display file preview", "snippet.go", "package main", "func main()"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered display preview %q missing %q", got, want)
		}
	}
}

func TestRenderReadFilePreviewIncludesLanguageInCaption(t *testing.T) {
	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}
	buffer.segments = []contentSegment{
		{
			kind: segmentToolCall,
			toolData: &toolCallSegment{
				tool:      "read",
				args:      "POEM.md",
				bodyKind:  "file",
				collapsed: false,
				preview: output.ToolPreview{
					Kind:     output.ToolPreviewKindReadFile,
					Path:     "POEM.md",
					Language: "markdown",
					Contents: "# The Quiet Code\n\nline one\n",
				},
			},
		},
	}

	got := stripANSI(buffer.String(80))
	for _, want := range []string{"POEM.md · read file preview · markdown ·", "read file preview · markdown"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered read preview %q missing %q", got, want)
		}
	}
}

func TestRenderToolPreviewKeepsGoSyntaxStyling(t *testing.T) {
	useTrueColor(t)

	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}

	doc := output.FormatFilePreview("snippet.go", "package main\nfunc main() {}\n")
	if len(doc.Lines) == 0 {
		t.Fatal("preview document has no lines")
	}

	got := buffer.renderPreviewLine(doc.Lines[0])
	plain := buffer.baseTextStyle().Render("package main")
	if got == plain {
		t.Fatalf("go keyword line rendered with plain style: %q", got)
	}
	if !strings.Contains(stripANSI(got), "package main") {
		t.Fatalf("rendered go line %q missing text", got)
	}
}

func TestRenderToolPreviewUsesStructuredDiffPreview(t *testing.T) {
	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}
	buffer.segments = []contentSegment{
		{
			kind: segmentToolCall,
			toolData: &toolCallSegment{
				tool:      "edit",
				args:      "internal/tui/content.go",
				bodyKind:  "diff",
				collapsed: false,
				preview: output.ToolPreview{
					Kind:   output.ToolPreviewKindEditDiff,
					Path:   "internal/tui/content.go",
					Before: "fmt.Println(\"old\")\n",
					After:  "fmt.Println(\"new\")\n",
				},
			},
		},
	}

	got := buffer.String(100)
	for _, want := range []string{"edit", "internal/tui/content.go", "+1", "-1", "old", "new"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered diff %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "[edit]") {
		t.Fatalf("rendered diff %q unexpectedly duplicated nested edit header", got)
	}
}

func TestRenderToolPreviewPreservesDiffSyntaxHighlighting(t *testing.T) {
	useTrueColor(t)

	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}

	doc := output.FormatEditDiffPreview("main.go", "package main\n", "package demo\n")
	lines := buffer.renderDiffPreviewDocument(doc, 100)
	if len(lines) != 3 {
		t.Fatalf("rendered diff lines = %d, want 3", len(lines))
	}

	want := buffer.previewTokenStyle(chroma.Keyword).Render("package")
	if !strings.Contains(lines[1], want) {
		t.Fatalf("removed diff line %q missing highlighted keyword %q", lines[1], want)
	}
	if !strings.Contains(lines[2], want) {
		t.Fatalf("added diff line %q missing highlighted keyword %q", lines[2], want)
	}
	if !hasANSIBackground(lines[1]) || !hasANSIBackground(lines[2]) {
		t.Fatalf("diff rows lost background styling: removed=%q added=%q", lines[1], lines[2])
	}
}

func TestRenderToolPreviewTrimsSharedMarkdownHeadingInDiffs(t *testing.T) {
	useTrueColor(t)

	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}

	doc := output.FormatEditDiffPreview("POEM.md", "# The Quiet Code\n\nOld body\n", "# The Quiet Code\n\nNew body\n")
	lines := buffer.renderDiffPreviewDocument(doc, 100)

	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "# The Quiet Code") {
		t.Fatalf("markdown diff %q still contains shared heading", joined)
	}
	if !strings.Contains(joined, "Old body") || !strings.Contains(joined, "New body") {
		t.Fatalf("markdown diff lines %v missing edited body text", lines)
	}
	if !hasANSIBackground(lines[2]) || !hasANSIBackground(lines[3]) {
		t.Fatalf("markdown diff lines lost row backgrounds: removed=%q added=%q", lines[2], lines[3])
	}
}

func TestRenderEditToolHeaderShowsDiffCountsBeforeCompletion(t *testing.T) {
	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "edit", "call_1", map[string]any{
		"path":       "POEM.md",
		"old_string": "old line\n",
		"new_string": "new line\n",
	}))

	if len(buffer.segments) != 1 || buffer.segments[0].toolData == nil {
		t.Fatalf("tool segments = %#v, want one populated tool segment", buffer.segments)
	}
	buffer.segments[0].toolData.collapsed = false

	got := buffer.String(100)
	if !strings.Contains(got, "+1") || !strings.Contains(got, "-1") {
		t.Fatalf("rendered diff header %q missing early diff counts", got)
	}
	if strings.Contains(got, "✅") {
		t.Fatalf("rendered diff header %q unexpectedly shows completion meta before finish", got)
	}
}

func TestRenderToolPreviewTruncatesLongFileBodies(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 30; i++ {
		body.WriteString("line\n")
	}

	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}
	buffer.segments = []contentSegment{
		{
			kind: segmentToolCall,
			toolData: &toolCallSegment{
				tool:      "read",
				args:      "notes.txt",
				bodyKind:  "file",
				collapsed: false,
				preview: output.ToolPreview{
					Kind:     output.ToolPreviewKindReadFile,
					Path:     "notes.txt",
					Contents: body.String(),
				},
			},
		},
	}

	got := buffer.String(80)
	if !strings.Contains(got, "… output truncated") && !strings.Contains(got, "↓ more") {
		t.Fatalf("rendered preview %q missing truncation marker", got)
	}
}

func TestRenderToolPreviewUsesStructuredListViews(t *testing.T) {
	tests := []struct {
		name    string
		tool    string
		args    string
		kind    string
		preview output.ToolPreview
		want    []string
	}{
		{
			name: "glob",
			tool: "glob",
			args: "**/*.go",
			kind: "glob",
			preview: output.ToolPreview{
				Kind:     output.ToolPreviewKindGlobList,
				Path:     "src",
				Returned: 2,
				Entries:  []output.ToolPreviewListEntry{{Path: "main.go"}, {Path: "pkg/tool.go"}},
			},
			want: []string{"glob results", "src", "main.go", "pkg/tool.go"},
		},
		{
			name: "ls",
			tool: "ls",
			args: "src",
			kind: "ls",
			preview: output.ToolPreview{
				Kind:     output.ToolPreviewKindLSList,
				Path:     "src",
				Returned: 2,
				Entries:  []output.ToolPreviewListEntry{{Path: "cmd", IsDir: true}, {Path: "main.go"}},
			},
			want: []string{"directory listing", "src", "cmd/", "main.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buffer := &contentBuffer{
				styles:        theme.BuildStyles(theme.AccentAmber),
				collapseState: make(map[int]bool),
			}
			buffer.segments = []contentSegment{
				{
					kind: segmentToolCall,
					toolData: &toolCallSegment{
						tool:      tt.tool,
						args:      tt.args,
						bodyKind:  tt.kind,
						collapsed: false,
						preview:   tt.preview,
					},
				},
			}

			got := buffer.String(100)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("rendered preview %q missing %q", got, want)
				}
			}
		})
	}
}

func TestRenderToolPreviewUsesStructuredGrepViews(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		preview output.ToolPreview
		want    []string
	}{
		{
			name: "content",
			kind: "grep",
			preview: output.ToolPreview{
				Kind:       output.ToolPreviewKindGrep,
				Path:       "src",
				OutputMode: "content",
				Returned:   1,
				GrepFiles: []output.ToolPreviewGrepFile{
					{
						Path: "src/main.go",
						Matches: []output.ToolPreviewGrepMatch{
							{LineNumber: 12, Text: "hello"},
							{LineNumber: 13, Text: "world"},
						},
					},
				},
			},
			want: []string{"content matches", "src/main.go", "hello", "world"},
		},
		{
			name: "files",
			kind: "grep",
			preview: output.ToolPreview{
				Kind:       output.ToolPreviewKindGrep,
				OutputMode: "files_with_matches",
				Returned:   2,
				GrepFiles:  []output.ToolPreviewGrepFile{{Path: "a.txt"}, {Path: "b.txt"}},
			},
			want: []string{"files with matches", "a.txt", "b.txt"},
		},
		{
			name: "count",
			kind: "grep",
			preview: output.ToolPreview{
				Kind:       output.ToolPreviewKindGrep,
				OutputMode: "count",
				Returned:   3,
				GrepFiles:  []output.ToolPreviewGrepFile{{Path: "a.txt", Count: 2}, {Path: "b.txt", Count: 1}},
			},
			want: []string{"match counts", "a.txt:2", "b.txt:1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buffer := &contentBuffer{
				styles:        theme.BuildStyles(theme.AccentAmber),
				collapseState: make(map[int]bool),
			}
			buffer.segments = []contentSegment{
				{
					kind: segmentToolCall,
					toolData: &toolCallSegment{
						tool:      "grep",
						args:      "needle",
						bodyKind:  tt.kind,
						collapsed: false,
						preview:   tt.preview,
					},
				},
			}

			got := buffer.String(100)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("rendered preview %q missing %q", got, want)
				}
			}
		})
	}
}

func TestRenderToolPreviewUsesStructuredBashView(t *testing.T) {
	tests := []struct {
		name    string
		preview output.ToolPreview
		want    []string
	}{
		{
			name: "exit code and truncation",
			preview: output.ToolPreview{
				Kind:      output.ToolPreviewKindBash,
				Command:   "go test ./...",
				Output:    "FAIL\n",
				Message:   "output truncated at 12 characters",
				ExitCode:  1,
				Truncated: true,
			},
			want: []string{"$ go test ./...", "FAIL", "exit 1", "output truncated"},
		},
		{
			name: "success",
			preview: output.ToolPreview{
				Kind:     output.ToolPreviewKindBash,
				Command:  "pwd",
				Output:   "/workspace\n",
				ExitCode: 0,
			},
			want: []string{"$ pwd", "/workspace", "exit 0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buffer := &contentBuffer{
				styles:        theme.BuildStyles(theme.AccentAmber),
				collapseState: make(map[int]bool),
			}
			buffer.segments = []contentSegment{
				{
					kind: segmentToolCall,
					toolData: &toolCallSegment{
						tool:      "bash",
						args:      tt.preview.Command,
						bodyKind:  "bash",
						collapsed: false,
						preview:   tt.preview,
					},
				},
			}

			got := buffer.String(100)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("rendered preview %q missing %q", got, want)
				}
			}
		})
	}
}

func lineHasHighlightedSpan(line output.PreviewLine) bool {
	for _, span := range line.Spans {
		if span.Type != chroma.Text {
			return true
		}
	}
	return false
}

func hasANSIBackground(s string) bool {
	return strings.Contains(s, "\x1b[48;2;") || strings.Contains(s, "\x1b[48;5;")
}

func useTrueColor(t *testing.T) {
	t.Helper()

	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(old)
	})
}
