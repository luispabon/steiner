package tui

import (
	"errors"
	"image/color"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/tui/theme"
	"github.com/luispabon/steiner/internal/usagestats"
)

func TestAdvisorToolCallSuppression(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	// Send ToolCallStarted for advisor — should NOT produce a segmentToolCall.
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "advisor", "call-advisor-1", nil))
	if len(buffer.segments) != 0 {
		t.Fatalf("segments count after ToolCallStarted = %d, want 0 (suppressed)", len(buffer.segments))
	}

	// Send ToolCallFinished for advisor — should not panic and not add a segment.
	buffer.AppendEvent(output.NewToolCallFinishedEvent(1, "advisor", "call-advisor-1", "done", nil))
	if len(buffer.segments) != 0 {
		t.Fatalf("segments count after ToolCallFinished = %d, want 0 (suppressed)", len(buffer.segments))
	}
}
func TestAppendEventDelegationStarted(t *testing.T) {
	t.Parallel()
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
	if !seg.delegData.promptCollapsed {
		t.Error("delegData.promptCollapsed = false, want true")
	}
}

func TestAppendEventDelegationComplete(t *testing.T) {
	t.Parallel()
	event := output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "child-2",
		Status:        "complete",
		TurnCount:     5,
		TokenCount:    2000,
		ToolCallCount: 0,
		Output:        "",
	})

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
	t.Parallel()
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
	t.Parallel()
	tests := []struct {
		name  string
		event output.Event
	}{
		{
			name:  "delegation_started",
			event: output.NewDelegationStartedEvent("agent-1", "secret task content here"),
		},
		{
			name: "delegation_complete",
			event: output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
				AgentID:       "agent-2",
				Status:        "complete",
				TurnCount:     1,
				TokenCount:    100,
				ToolCallCount: 0,
				Output:        "",
			}),
		},
		{
			name:  "delegation_failed",
			event: output.NewDelegationFailedEvent("agent-3", "secret task", "error details"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
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

func TestAppendEventSuppressesTransportOverrideProviderDiagnostic(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:     make([]contentSegment, 0),
		streaming:    true,
		streamBuffer: "assistant chunk in progress",
	}

	buffer.AppendEvent(output.NewTransportDiagnosticEvent("model-a", "configured", "effective", "fallback", "override selected"))

	if len(buffer.segments) != 0 {
		t.Fatalf("segments count = %d, want 0 for suppressible transport diagnostic", len(buffer.segments))
	}
	if !buffer.streaming {
		t.Fatal("streaming = false, want active stream to remain untouched")
	}
	if got := buffer.streamBuffer; got != "assistant chunk in progress" {
		t.Fatalf("streamBuffer = %q, want unchanged streaming buffer", got)
	}
}

func TestAppendEventRendersWarningProviderDiagnostic(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	buffer.AppendEvent(output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
		Turn:     8,
		Severity: "warning",
		Message:  "provider returned transient error, retrying turn in 5s",
	}))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1 for warning diagnostic", len(buffer.segments))
	}
	seg := buffer.segments[0]
	if seg.kind != segmentStatus {
		t.Fatalf("segment kind = %v, want segmentStatus", seg.kind)
	}
	if !strings.Contains(seg.text, "provider turn=8 warning message=provider returned transient error, retrying turn in 5s") {
		t.Fatalf("segment text = %q, want rendered provider warning", seg.text)
	}
}

func TestFormatDelegationEvent(t *testing.T) {
	t.Parallel()
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
			name: "complete",
			event: output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
				AgentID:       "test-agent",
				Status:        "complete",
				TurnCount:     2,
				TokenCount:    500,
				ToolCallCount: 0,
				Output:        "",
			}),
			wantMatch: "delegate: complete test-agent (2 turns)",
		},
		{
			name: "complete_has_tool_calls",
			event: output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
				AgentID:       "test-agent",
				Status:        "complete",
				TurnCount:     2,
				TokenCount:    500,
				ToolCallCount: 3,
				Output:        "",
			}),
			wantMatch: "delegate: complete test-agent (2 turns, 3 tool calls)",
		},
		{
			name:      "failed",
			event:     output.NewDelegationFailedEvent("test-agent", "task", "err"),
			wantMatch: "delegate: failed test-agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := formatDelegationEvent(tt.event)
			if result != tt.wantMatch {
				t.Errorf("formatDelegationEvent = %q, want %q", result, tt.wantMatch)
			}
		})
	}
}

func TestAppendEventAdvisorLifecycle(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 2, "", nil))
	if len(buffer.segments) != 1 {
		t.Fatalf("segments count after start = %d, want 1", len(buffer.segments))
	}
	seg := buffer.segments[0]
	if seg.kind != segmentDelegation || seg.delegData == nil {
		t.Fatalf("segment = %#v, want advisor delegation box", seg)
	}
	if !seg.delegData.isAdvisor {
		t.Fatal("delegData.isAdvisor = false, want true")
	}
	if got := seg.delegData.status; got != "active" {
		t.Fatalf("status after start = %q, want active", got)
	}

	buffer.AppendEvent(output.NewAdvisorCompleteEvent(output.AdvisorCompleteParams{Model: "advisor-model", UseNumber: 1, MaxUses: 2, Note: "check tests first"}))
	seg = buffer.segments[0]
	if got := seg.delegData.status; got != "complete" {
		t.Fatalf("status after complete = %q, want complete", got)
	}
	if got := seg.delegData.output; got != "check tests first" {
		t.Fatalf("output after complete = %q, want advisor note", got)
	}
	if seg.delegData.cacheHitOK {
		t.Fatal("cacheHitOK = true, want false for a payload with no cache token fields")
	}

	// After complete, buffer should have 5 segments: advisor box + 3 labeled-block segments + blank margin.
	if len(buffer.segments) != 5 {
		t.Fatalf("segments count after complete = %d, want 5 (advisor box + labeled block + blank margin)", len(buffer.segments))
	}

	// Index 1: opening separator with label "Advisor output", closing=false.
	s := buffer.segments[1]
	if s.kind != segmentSeparator || s.separatorData == nil {
		t.Fatalf("segment[1] kind=%v, want segmentSeparator with separatorData", s.kind)
	}
	if s.separatorData.label != "Advisor output" {
		t.Errorf("segment[1] label = %q, want %q", s.separatorData.label, "Advisor output")
	}
	if s.separatorData.closing {
		t.Errorf("segment[1] closing = true, want false")
	}

	// Index 2: body segment (markdown).
	s = buffer.segments[2]
	if s.kind != segmentAssistantMarkdown {
		t.Fatalf("segment[2] kind=%v, want segmentAssistantMarkdown", s.kind)
	}
	if s.text != "check tests first" {
		t.Errorf("segment[2] text = %q, want %q", s.text, "check tests first")
	}

	// Index 3: closing separator with label "Advisor output", closing=true.
	s = buffer.segments[3]
	if s.kind != segmentSeparator || s.separatorData == nil {
		t.Fatalf("segment[3] kind=%v, want segmentSeparator with separatorData", s.kind)
	}
	if s.separatorData.label != "Advisor output" {
		t.Errorf("segment[3] label = %q, want %q", s.separatorData.label, "Advisor output")
	}
	if !s.separatorData.closing {
		t.Errorf("segment[3] closing = false, want true")
	}
	// Index 4: blank margin segment.
	s = buffer.segments[4]
	if s.kind != segmentPlain {
		t.Fatalf("segment[4] kind=%v, want segmentPlain", s.kind)
	}
	if s.text != " " {
		t.Errorf("segment[4] text = %q, want single space", s.text)
	}

	buffer.AppendEvent(output.NewAdvisorBudgetExhaustedEvent("advisor-model", 2, 2, "advisor budget exhausted for this session (2/2); proceed on your own judgment", "", nil))
	if len(buffer.segments) != 6 {
		t.Fatalf("segments count after budget event = %d, want 6", len(buffer.segments))
	}
	last := buffer.segments[5]
	if last.delegData == nil || last.delegData.status != "budget_exhausted" {
		t.Fatalf("last advisor segment = %#v, want budget_exhausted", last.delegData)
	}
}

func TestAppendEventAdvisorLifecycleFailure(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 2, "", nil))
	if len(buffer.segments) != 1 {
		t.Fatalf("segments count after start = %d, want 1", len(buffer.segments))
	}

	// Complete with an error.
	buffer.AppendEvent(output.NewAdvisorCompleteEvent(output.AdvisorCompleteParams{Model: "advisor-model", UseNumber: 1, MaxUses: 2, Err: errors.New("something went wrong")}))

	if len(buffer.segments) != 5 {
		t.Fatalf("segments count after complete with error = %d, want 5 (advisor box + labeled block + blank margin)", len(buffer.segments))
	}

	// Index 0: advisor box with status "failed" and isAdvisor.
	seg := buffer.segments[0]
	if seg.kind != segmentDelegation || seg.delegData == nil {
		t.Fatalf("segment[0] kind=%v, want segmentDelegation", seg.kind)
	}
	if !seg.delegData.isAdvisor {
		t.Error("segment[0] delegData.isAdvisor = false, want true")
	}
	if seg.delegData.status != "failed" {
		t.Errorf("segment[0] delegData.status = %q, want %q", seg.delegData.status, "failed")
	}
	if seg.delegData.cacheHitOK {
		t.Error("segment[0] delegData.cacheHitOK = true, want false on error path")
	}

	// Index 1: opening separator with label "Advisor output", closing=false.
	s := buffer.segments[1]
	if s.kind != segmentSeparator || s.separatorData == nil {
		t.Fatalf("segment[1] kind=%v, want segmentSeparator with separatorData", s.kind)
	}
	if s.separatorData.label != "Advisor output" {
		t.Errorf("segment[1] label = %q, want %q", s.separatorData.label, "Advisor output")
	}
	if s.separatorData.closing {
		t.Errorf("segment[1] closing = true, want false")
	}

	// Index 2: body segment containing the error text.
	s = buffer.segments[2]
	if s.kind != segmentAssistantMarkdown {
		t.Fatalf("segment[2] kind=%v, want segmentAssistantMarkdown", s.kind)
	}
	if s.text != "something went wrong" {
		t.Errorf("segment[2] text = %q, want %q", s.text, "something went wrong")
	}

	// Index 3: closing separator with label "Advisor output", closing=true.
	s = buffer.segments[3]
	if s.kind != segmentSeparator || s.separatorData == nil {
		t.Fatalf("segment[3] kind=%v, want segmentSeparator with separatorData", s.kind)
	}
	if s.separatorData.label != "Advisor output" {
		t.Errorf("segment[3] label = %q, want %q", s.separatorData.label, "Advisor output")
	}
	if !s.separatorData.closing {
		t.Errorf("segment[3] closing = false, want true")
	}
}

func TestAdvisorThinkingChunkRouting(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		showThinking:  true,
	}

	// Emit advisor started.
	buffer.AppendEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 2, "", nil))
	if len(buffer.segments) != 1 {
		t.Fatalf("segments after start = %d, want 1", len(buffer.segments))
	}

	// Emit thinking chunks with advisor source.
	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(0, "thinking step one ", output.ChunkSourceAdvisor))
	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(0, "thinking step two", output.ChunkSourceAdvisor))

	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want advisor delegation state")
	}
	if !dd.isAdvisor {
		t.Fatal("isAdvisor = false, want true")
	}

	// Entries should contain merged thinking transcript.
	if got := len(dd.entries); got != 1 {
		t.Fatalf("entries count = %d, want 1 (merged thinking)", got)
	}
	if got := dd.entries[0].kind; got != delegationTranscriptEntryThinking {
		t.Fatalf("entry kind = %v, want delegationTranscriptEntryThinking", got)
	}
	if got := dd.entries[0].body; got != "thinking step one thinking step two" {
		t.Fatalf("entry body = %q, want merged thinking content", got)
	}

	// Emit complete with output.
	buffer.AppendEvent(output.NewAdvisorCompleteEvent(output.AdvisorCompleteParams{Model: "advisor-model", UseNumber: 1, MaxUses: 2, Note: "Final advice here"}))

	// Verify the advisor box status is complete.
	if got := dd.status; got != "complete" {
		t.Fatalf("status after complete = %q, want complete", got)
	}

	// Verify the labeled block is still appended (index 1).
	if len(buffer.segments) < 2 {
		t.Fatal("segments too few after complete, want labeled block")
	}
	if buffer.segments[1].kind != segmentSeparator || buffer.segments[1].separatorData == nil {
		t.Fatalf("segment[1] kind=%v, want segmentSeparator", buffer.segments[1].kind)
	}
	if buffer.segments[1].separatorData.label != "Advisor output" {
		t.Errorf("segment[1] label = %q, want %q", buffer.segments[1].separatorData.label, "Advisor output")
	}
}
func TestRenderAdvisorTrailingMargin(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 2, "check the layout", []string{"internal/tui/content_render_delegation.go"}))
	buffer.AppendEvent(output.NewAdvisorCompleteEvent(output.AdvisorCompleteParams{Model: "advisor-model", UseNumber: 1, MaxUses: 2, Note: "some advisor note"}))

	rendered := stripANSI(buffer.String(80))

	// The rendered output should end with a blank line after the closing separator.
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")

	// Find the closing separator line.
	var sepIdx int
	for i, line := range lines {
		if strings.Contains(line, "End of Advisor output") {
			sepIdx = i
			break
		}
	}
	if sepIdx == 0 {
		t.Fatalf("closing separator not found in rendered output:\n%s", rendered)
	}

	// The line after the closing separator should be blank (trailing margin).
	if sepIdx+1 >= len(lines) || strings.TrimSpace(lines[sepIdx+1]) != "" {
		t.Fatalf("expected blank line after closing separator, got %q:\n%s", lines[sepIdx+1], rendered)
	}
}

func TestAppendCompactionResultLabeledBlock(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	buffer.AppendCompactionResult("Compaction", "summary text")

	if len(buffer.segments) != 3 {
		t.Fatalf("segments count = %d, want 3", len(buffer.segments))
	}

	// Index 0: opening separator with label "Compaction", closing=false.
	s := buffer.segments[0]
	if s.kind != segmentSeparator || s.separatorData == nil {
		t.Fatalf("segment[0] kind=%v, want segmentSeparator with separatorData", s.kind)
	}
	if s.separatorData.label != "Compaction" {
		t.Errorf("segment[0] label = %q, want %q", s.separatorData.label, "Compaction")
	}
	if s.separatorData.closing {
		t.Errorf("segment[0] closing = true, want false")
	}

	// Index 1: body segment.
	s = buffer.segments[1]
	if s.kind != segmentAssistantMarkdown {
		t.Fatalf("segment[1] kind=%v, want segmentAssistantMarkdown", s.kind)
	}
	if s.text != "summary text" {
		t.Errorf("segment[1] text = %q, want %q", s.text, "summary text")
	}

	// Index 2: closing separator with label "Compaction", closing=true.
	s = buffer.segments[2]
	if s.kind != segmentSeparator || s.separatorData == nil {
		t.Fatalf("segment[2] kind=%v, want segmentSeparator with separatorData", s.kind)
	}
	if s.separatorData.label != "Compaction" {
		t.Errorf("segment[2] label = %q, want %q", s.separatorData.label, "Compaction")
	}
	if !s.separatorData.closing {
		t.Errorf("segment[2] closing = false, want true")
	}
}

func TestDelegationSpinnerAdvancement(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
			t.Parallel()
			got := formatElapsed(tt.startNano, tt.endNano)
			if got != tt.want {
				t.Errorf("formatElapsed(%d, %d) = %q, want %q", tt.startNano, tt.endNano, got, tt.want)
			}
		})
	}
}

func TestDelegationLifecycle(t *testing.T) {
	t.Parallel()
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
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "life-agent",
		Status:        "done",
		TurnCount:     3,
		TokenCount:    600,
		ToolCallCount: 0,
		Output:        "",
	}))
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
	t.Parallel()
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
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "toggle-agent",
		Status:        "done",
		TurnCount:     1,
		TokenCount:    50,
		ToolCallCount: 0,
		Output:        "result text",
	}))

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

	// Toggle again → collapsed; output text hidden.
	buffer.ToggleLastDelegationOutput()
	if !dd.collapsed {
		t.Error("delegation should be collapsed after second toggle")
	}
	rendered = buffer.String(80)
	if strings.Contains(rendered, "result text") {
		t.Errorf("rendered output should not contain 'result text' in collapsed state: %q", rendered)
	}
}

func TestDelegationPromptStateFromParentCall(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	task := strings.Repeat("inspect docs carefully ", 6)
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "inspect docs"))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call_delegate_1", map[string]any{
		"task": task,
	}))

	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil")
	}
	if got := dd.promptText; got != task {
		t.Fatalf("promptText = %q, want full task %q", got, task)
	}
	if got := dd.parentArgs; got != task {
		t.Fatalf("parentArgs = %q, want full task text %q (truncation happens at render time)", got, task)
	}
}

func TestDelegationPromptStateWithParentCallBeforeStarted(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	task := strings.Repeat("inspect docs carefully ", 6)
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call_delegate_1", map[string]any{"task": task}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "inspect docs"))

	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil")
	}
	if got := dd.promptText; got != task {
		t.Fatalf("promptText = %q, want full task %q", got, task)
	}
	if dd.promptCollapsed != true {
		t.Fatal("promptCollapsed = false, want true")
	}
}

func TestDelegationExpandedOutputIsNotTruncated(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}
	longOutput := strings.Repeat("x", 650) + "tail marker"

	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "long-agent",
		Status:        "done",
		TurnCount:     1,
		TokenCount:    50,
		ToolCallCount: 0,
		Output:        longOutput,
	}))
	buffer.ToggleLastDelegationOutput()

	rendered := buffer.String(80)
	if !strings.Contains(rendered, "tail") || !strings.Contains(rendered, "marker") {
		t.Fatalf("expanded delegation output was truncated: %q", rendered)
	}
}

func TestDelegationBlockRendering(t *testing.T) {
	t.Parallel()
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
			name: "complete_has_agent_id",
			setup: func(b *contentBuffer) {
				b.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
					AgentID:       "c-agent",
					Status:        "done",
					TurnCount:     7,
					TokenCount:    300,
					ToolCallCount: 3,
					Output:        "",
				}))
			},
			checks: []string{"c-agent"},
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
			t.Parallel()
			buffer := &contentBuffer{
				segments:      make([]contentSegment, 0),
				collapseState: make(map[int]bool),
				styles:        testStyles(theme.AccentAmber),
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

func TestRenderDelegationCollapsedActiveShowsSpinnerAndLatestOperation(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call_delegate_1", map[string]any{
		"task": "initial task preview",
	}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "initial task preview"))
	buffer.AppendEvent(output.WithAgentScope(
		output.NewToolCallStartedEvent(1, "read", "call_1", map[string]any{"path": "README.md"}),
		"child-1",
	))

	rendered := buffer.String(80)
	for _, want := range []string{"explore", "child-1", "⠋", "read: README.md:1–200"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("collapsed active delegation render %q missing %q", rendered, want)
		}
	}
}

func TestRenderDelegationExpandedShowsAssistantAndLightweightToolRows(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call_delegate_1", map[string]any{
		"task": "inspect docs",
	}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "inspect docs"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", "child assistant reply"), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(
		output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "pwd"}),
		"child-1",
	))
	buffer.AppendEvent(output.WithAgentScope(
		output.NewToolCallFinishedEvent(1, "bash", "call_1", "ok", nil),
		"child-1",
	))
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "child-1",
		Status:        "complete",
		TurnCount:     2,
		TokenCount:    25,
		ToolCallCount: 0,
		Output:        "final child output",
	}))
	buffer.ToggleLastDelegationOutput()

	rendered := buffer.String(80)
	for _, want := range []string{"explore", "child-1", "prompt", "child assistant reply", "bash", "pwd", "✓", "output", "final child output"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expanded delegation render %q missing %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "┌") && strings.Contains(rendered, "bash") && strings.Contains(rendered, "pwd") && strings.Count(rendered, "┌") > 1 {
		t.Fatalf("expanded delegation child tool should not render like a nested boxed tool call: %q", rendered)
	}
}

func TestRenderDelegationPromptSubsectionCollapsedAndExpanded(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}

	prompt := "inspect the prompt layout\nwith a line that wraps nicely"
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call_delegate_1", map[string]any{
		"task": prompt,
	}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", prompt))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", "child assistant reply"), "child-1"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "child-1",
		Status:        "complete",
		TurnCount:     1,
		TokenCount:    10,
		ToolCallCount: 0,
		Output:        "final child output",
	}))

	buffer.ToggleLastDelegationOutput()
	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil")
	}
	dd.collapsed = false
	dd.promptCollapsed = false
	buffer.segments[0].renderDirty = true

	renderedCollapsed := stripANSI(buffer.String(42))
	if !strings.Contains(renderedCollapsed, "▾ prompt") {
		t.Fatalf("expanded prompt render %q missing subsection header", renderedCollapsed)
	}

	dd.promptCollapsed = true
	buffer.segments[0].renderDirty = true
	renderedPreview := buffer.String(42)
	if !strings.Contains(renderedPreview, "▸ prompt") {
		t.Fatalf("collapsed prompt render %q missing subsection header", renderedPreview)
	}
	if strings.Contains(renderedPreview, "inspect the prompt layout with a long line") {
		t.Fatalf("collapsed prompt render leaked full prompt text: %q", renderedPreview)
	}

	dd.promptCollapsed = false
	buffer.segments[0].renderDirty = true
	renderedExpanded := stripANSI(buffer.String(80))
	if !strings.Contains(renderedExpanded, "▾ prompt") {
		t.Fatalf("expanded prompt render %q missing expanded subsection header", renderedExpanded)
	}
	lines := strings.Split(renderedExpanded, "\n")
	promptLine := -1
	assistantLine := -1
	for i, line := range lines {
		if promptLine == -1 && strings.Contains(line, "▾ prompt") {
			promptLine = i
		}
		if assistantLine == -1 && strings.Contains(line, "│ child assistant reply") {
			assistantLine = i
		}
	}
	if promptLine == -1 || assistantLine == -1 || assistantLine <= promptLine {
		t.Fatalf("expanded prompt render %q ordered incorrectly", renderedExpanded)
	}
	if idxPrompt := strings.Index(renderedExpanded, "prompt"); idxPrompt == -1 || strings.Index(renderedExpanded, "│ child assistant reply") < idxPrompt {
		t.Fatalf("expanded prompt render %q ordered incorrectly", renderedExpanded)
	}
}

func TestRenderDelegationBlankPromptSkipsSubsection(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", ""))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call_delegate_1", map[string]any{
		"task": "",
	}))
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "child-1",
		Status:        "complete",
		TurnCount:     1,
		TokenCount:    10,
		ToolCallCount: 0,
		Output:        "final child output",
	}))
	buffer.ToggleLastDelegationOutput()

	rendered := buffer.String(80)
	if strings.Contains(rendered, "prompt") {
		t.Fatalf("blank prompt render %q should skip subsection", rendered)
	}
}

func TestRenderNormalParentBashToolRemainsBoxed(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call_bash_1", map[string]any{
		"command": "pwd",
	}))

	rendered := buffer.String(80)
	for _, want := range []string{"┌", "┐", "bash", "pwd"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("normal parent bash render %q missing %q", rendered, want)
		}
	}
}

func TestRenderDelegationLifecycleUsesSingleBoxSegment(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call_delegate_1", map[string]any{
		"task": "inspect docs",
	}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "inspect docs"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "child-1",
		Status:        "complete",
		TurnCount:     1,
		TokenCount:    10,
		ToolCallCount: 0,
		Output:        "",
	}))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	if got := buffer.segments[0].kind; got != segmentDelegation {
		t.Fatalf("segment kind = %v, want segmentDelegation", got)
	}

	rendered := buffer.String(80)
	for _, want := range []string{"explore", "child-1", "✓", "complete"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("delegation lifecycle render %q missing %q", rendered, want)
		}
	}
}

func TestRenderDelegationTranscriptTruncatesToRecentRows(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		styles:        testStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}

	entries := make([]delegationTranscriptEntry, 0, 50)
	for i := 0; i < 50; i++ {
		entries = append(entries, delegationTranscriptEntry{
			kind: delegationTranscriptEntryAssistant,
			body: "line " + strconv.Itoa(i),
		})
	}
	buffer.segments = []contentSegment{{
		kind: segmentDelegation,
		delegData: &delegationDisplayState{
			agentID:      "child-1",
			status:       "complete",
			resultStatus: "complete",
			collapsed:    false,
			entries:      entries,
		},
	}}

	rendered := buffer.String(80)
	if !strings.Contains(rendered, "[old child events hidden]") {
		t.Fatalf("rendered delegation transcript %q missing truncation marker", rendered)
	}
	if strings.Contains(rendered, "line 0") {
		t.Fatalf("rendered delegation transcript should hide oldest rows: %q", rendered)
	}
	if !strings.Contains(rendered, "line 49") {
		t.Fatalf("rendered delegation transcript %q missing most recent row", rendered)
	}
}

func TestAppendEventContextDiagnosticsAreVisible(t *testing.T) {
	t.Parallel()
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

	// session_health events no longer produce content buffer segments; they only
	// update the status context fragment.
	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	if buffer.segments[0].kind != segmentCompactionBanner {
		t.Fatalf("segment[0] kind = %v, want segmentCompactionBanner", buffer.segments[0].kind)
	}
	if buffer.segments[0].compactionData == nil {
		t.Fatalf("segment[0] compactionData is nil")
	}
	if !strings.Contains(buffer.segments[0].compactionData.summary, "compacted") {
		t.Fatalf("compaction summary = %q, want visible compaction data", buffer.segments[0].compactionData.summary)
	}
}

func TestAppendEventContextReportRendersMarkdownBlock(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	buffer.AppendEvent(output.NewOverlayReportEvent("Context Report", "# Last Request Context\n\nPrompt tokens: `42`"))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	if got := buffer.segments[0].text; !strings.Contains(got, "Last Request Context") || !strings.Contains(got, "Prompt tokens") {
		t.Fatalf("segment text = %q, want context report block", got)
	}
}

func TestAppendEventStreamsThinkingAndAssistantChunks(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "internal reasoning", output.ChunkSourceAssistant))
	buffer.AppendEvent(output.NewAssistantChunkEventWithSource(1, `{"intent":"inspect","next":"read file"}`, output.ChunkSourceAssistant))
	buffer.AppendEvent(output.NewAssistantChunkEventWithSource(1, "visible answer", output.ChunkSourceAssistant))
	buffer.finishStreaming()

	if len(buffer.segments) != 2 {
		t.Fatalf("segments count = %d, want 2", len(buffer.segments))
	}
	if buffer.segments[0].kind != segmentThinkingBlock {
		t.Fatalf("segment[0].kind = %v, want segmentThinkingBlock", buffer.segments[0].kind)
	}
	if got := buffer.segments[1].text; !strings.Contains(got, `"intent":"inspect"`) || !strings.Contains(got, "visible answer") {
		t.Fatalf("assistant segment = %q, want streamed assistant content", got)
	}
}

func TestThinkingBlocksStartExpandedWhileStreamingAndCollapseWhenFinished(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
		showThinking:  true,
	}

	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "first line", output.ChunkSourceAssistant))
	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "\nsecond line", output.ChunkSourceAssistant))

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
	for _, want := range []string{"▾ first line", "second line"} {
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
	// Collapsed format keeps first line and marks continuation with an ellipsis.
	plain := stripANSI(renderedCollapsed)
	if !strings.Contains(plain, "▸ first line") || !strings.Contains(plain, "…") {
		t.Fatalf("collapsed render = %q, want '▸ first line' with ellipsis", plain)
	}
	if strings.Contains(plain, "second line") {
		t.Fatalf("collapsed render = %q, must not contain %q", plain, "second line")
	}
}

func TestThinkingChunksRenderSeparateItems(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
		showThinking:  true,
	}

	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "**Inspecting configuration**", output.ChunkSourceAssistant))
	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "\n**Planning safe automation**", output.ChunkSourceAssistant))

	thinking := buffer.segments[0].thinkData
	if thinking == nil {
		t.Fatal("thinkData = nil, want thinking block")
	}
	if got := thinking.body; got != "**Inspecting configuration**\n**Planning safe automation**" {
		t.Fatalf("thinkData.body = %q, want %q", got, "**Inspecting configuration**\n**Planning safe automation**")
	}
	plain := stripANSI(buffer.String(80))
	if strings.Contains(plain, "**") {
		t.Fatalf("render contains markdown bold markers: %q", plain)
	}
	for _, want := range []string{"Inspecting configuration", "Planning safe automation"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("render = %q, want %q", plain, want)
		}
	}
	buffer.finalizeThinkingBlock()
	plain = stripANSI(buffer.String(80))
	if !strings.Contains(plain, "▸ Inspecting configuration") {
		t.Fatalf("collapsed render = %q, want %q", plain, "▸ Inspecting configuration")
	}
	if strings.Contains(plain, "Planning safe automation") {
		t.Fatalf("collapsed render = %q, must not contain %q", plain, "Planning safe automation")
	}
	if strings.Contains(plain, "**") {
		t.Fatalf("collapsed render contains markdown bold markers: %q", plain)
	}
}

func TestThinkingChunkSplitMarkerStripped(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
		showThinking:  true,
	}

	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "*", output.ChunkSourceAssistant))
	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "*Planning safe automation", output.ChunkSourceAssistant))

	thinking := buffer.segments[0].thinkData
	if thinking == nil {
		t.Fatal("thinkData = nil, want thinking block")
	}
	if got := thinking.body; got != "**Planning safe automation" {
		t.Fatalf("thinkData.body = %q, want %q", got, "**Planning safe automation")
	}
	plain := stripANSI(buffer.String(80))
	if strings.Contains(plain, "**") {
		t.Fatalf("render contains markdown bold markers: %q", plain)
	}
	if !strings.Contains(plain, "Planning safe automation") {
		t.Fatalf("render = %q, want %q", plain, "Planning safe automation")
	}
}

func TestStripThinkingMarkers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "balanced bold", in: "**bold**", want: "bold"},
		{name: "literal star preserved", in: "a * literal * star", want: "a * literal * star"},
		{name: "unbalanced", in: "**unbalanced", want: "unbalanced"},
		{name: "only markers", in: "****", want: ""},
		{name: "no markers", in: "no markers", want: "no markers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := stripThinkingMarkers(tt.in); got != tt.want {
				t.Fatalf("stripThinkingMarkers(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestThinkingBlockBeforeToolCallStartsToolBoxOnFreshLine(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
		showThinking:  true,
	}

	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "inspect renderer", output.ChunkSourceAssistant))
	buffer.finalizeThinkingBlock()
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "read", "call_1", map[string]any{"path": "internal/tui/content_tool.go"}))

	rendered := stripANSI(buffer.String(80))
	lines := strings.Split(rendered, "\n")

	thinkingLine := -1
	for i, line := range lines {
		if strings.Contains(line, "inspect renderer") {
			thinkingLine = i
			break
		}
	}
	if thinkingLine == -1 {
		t.Fatalf("rendered output missing thinking line: %q", rendered)
	}
	toolBoxLine := -1
	for i := thinkingLine + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "┌") {
			toolBoxLine = i
			break
		}
	}
	if toolBoxLine == -1 {
		t.Fatalf("rendered output missing tool box border after thinking block: %q", rendered)
	}
}

func TestAPIResponseFinalizesAssistantChunksAfterThinking(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
		showThinking:  true,
	}

	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "internal reasoning", output.ChunkSourceAssistant))
	buffer.AppendEvent(output.NewAssistantChunkEventWithSource(1, `{"intent":"inspect","next":"read file"}`, output.ChunkSourceAssistant))
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
		t.Fatalf("assistant segment = %q, want finalized assistant text", got)
	}
}

func TestThinkingBlockSurvivesInterleavedContentChunk(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
		showThinking:  true,
	}

	// Simulate a turn where thinking chunks interleave with content chunks,
	// as happens with deepseek-v4-flash: thinking, content, thinking.
	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "first reasoning", output.ChunkSourceAssistant))
	buffer.AppendEvent(output.NewAssistantChunkEventWithSource(1, "visible content", output.ChunkSourceAssistant))
	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, " more reasoning", output.ChunkSourceAssistant))

	// Should have exactly 1 thinking block segment, not 2.
	if got := len(buffer.segments); got != 1 {
		t.Fatalf("segments count = %d, want 1 (single thinking block)", got)
	}

	if buffer.segments[0].kind != segmentThinkingBlock {
		t.Fatalf("segment[0].kind = %v, want segmentThinkingBlock", buffer.segments[0].kind)
	}

	thinkData := buffer.segments[0].thinkData
	if thinkData == nil {
		t.Fatal("thinkData = nil, want thinking block")
	}

	// Verify the merged thinking block contains both reasoning chunks.
	expectedBody := "first reasoning more reasoning"
	if got := thinkData.body; got != expectedBody {
		t.Fatalf("thinkData.body = %q, want %q", got, expectedBody)
	}

	// Verify stream buffer contains the visible content.
	if got := strings.TrimSpace(buffer.streamBuffer); !strings.Contains(got, "visible content") {
		t.Fatalf("streamBuffer = %q, want to contain 'visible content'", got)
	}
}

func TestRenderThinkingBlockSegment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		width     int
		thinkData *thinkingBlockData
		want      []string
		notWant   []string
		exact     string
	}{
		{name: "nil thinkData", width: 80, thinkData: nil, want: []string{""}, notWant: []string{"Thinking"}},
		{name: "expanded state, short text", width: 80, thinkData: &thinkingBlockData{body: "short thought"}, want: []string{"▾ short thought"}, notWant: []string{"▸", "…", "▎", "Thinking"}},
		{name: "expanded state, long line wraps", width: 40, thinkData: &thinkingBlockData{body: strings.Repeat("a", 80)}, want: []string{"▾"}, notWant: []string{"▸", "Thinking"}},
		{name: "expanded state, empty body", width: 80, thinkData: &thinkingBlockData{}, want: []string{"▾"}, notWant: []string{"Thinking"}, exact: "▾"},
		{name: "collapsed state, empty body", width: 80, thinkData: &thinkingBlockData{collapsed: true}, want: []string{"▸"}, notWant: []string{"▸ ", "…", "Thinking"}, exact: "▸"},
		{name: "collapsed state, single line fits, no ellipsis", width: 80, thinkData: &thinkingBlockData{body: "short thought", collapsed: true, streaming: false}, want: []string{"▸ short thought"}, notWant: []string{"…"}, exact: "▸ short thought"},
		{name: "collapsed state, blank first line with more lines", width: 80, thinkData: &thinkingBlockData{body: "\nsecond line", collapsed: true}, want: []string{"▸ …"}, notWant: []string{"second line", "Thinking"}},
		{name: "collapsed state, exact fill with more lines", width: 20, thinkData: &thinkingBlockData{body: strings.Repeat("a", 18) + "\nmore", collapsed: true}, want: []string{"▸ " + strings.Repeat("a", 17) + "…"}, notWant: []string{"more", "Thinking"}},
		{name: "expanded state, continuation indent", width: 40, thinkData: &thinkingBlockData{body: strings.Repeat("a", 60) + "\nlast line"}, want: []string{"▾ ", "last line"}, notWant: []string{"Thinking"}},
		{name: "collapsed state, 2 lines", width: 80, thinkData: &thinkingBlockData{body: "first line\nsecond line", collapsed: true}, want: []string{"▸ first line…"}, notWant: []string{"second line", "▾", "Thinking"}},
		{name: "collapsed state, 5 lines", width: 80, thinkData: &thinkingBlockData{body: "line1\nline2\nline3\nline4\nline5", collapsed: true}, want: []string{"▸ line1…"}, notWant: []string{"line2", "line3", "line4", "line5", "▾", "Thinking"}},
		{name: "collapsed state at wide width", width: 120, thinkData: &thinkingBlockData{body: "first\nsecond\nthird", collapsed: true}, want: []string{"▸ first…"}, notWant: []string{"second", "third", "▾", "Thinking"}},
		{name: "collapsed state, long first line truncates to width", width: 20, thinkData: &thinkingBlockData{body: strings.Repeat("a", 100) + "\nmore lines", collapsed: true}, want: []string{"▸ a", "…"}, notWant: []string{"more lines", "▾", "Thinking"}},
		{name: "collapsed state, degenerate width", width: 2, thinkData: &thinkingBlockData{body: "short thought", collapsed: true}, want: []string{"▸"}, notWant: []string{"▸ ", "short thought", "Thinking"}, exact: "▸"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := &contentBuffer{styles: testStyles(theme.AccentAmber)}
			result := buf.renderThinkingBlockSegment(contentSegment{kind: segmentThinkingBlock, thinkData: tc.thinkData}, tc.width)
			plain := stripANSI(result)
			for _, w := range tc.want {
				if w == "" {
					if plain != "" {
						t.Errorf("result should be empty, got: %q", plain)
					}
					continue
				}
				if !strings.Contains(plain, w) {
					t.Errorf("result missing %q\ngot: %q", w, plain)
				}
			}
			for _, n := range tc.notWant {
				if strings.Contains(plain, n) {
					t.Errorf("result should not contain %q\ngot: %q", n, plain)
				}
			}
			if tc.exact != "" && strings.TrimSpace(plain) != tc.exact {
				t.Errorf("trimmed result = %q, want %q", strings.TrimSpace(plain), tc.exact)
			}
			for _, line := range strings.Split(plain, "\n") {
				if len([]rune(line)) > tc.width {
					t.Errorf("line exceeds width %d: %q", tc.width, line)
				}
			}
			if tc.name == "expanded state, continuation indent" {
				lines := strings.Split(plain, "\n")
				if len(lines) < 3 || !strings.HasPrefix(lines[0], "▾ ") || !strings.HasPrefix(lines[1], "  ") || !strings.Contains(lines[2], "last line") {
					t.Errorf("continuation render = %q, want marker, indented continuation, and last line", plain)
				}
			}
			if tc.name == "collapsed state, exact fill with more lines" {
				line := strings.Split(plain, "\n")[0]
				if len([]rune(line)) != 20 {
					t.Errorf("exact-fill line length = %d, want 20: %q", len([]rune(line)), line)
				}
			}
		})
	}
}
func TestAppendUserMarkdownSegmentKind(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
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
	t.Parallel()
	b := &contentBuffer{
		styles:        testStyles(theme.AccentAmber),
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
	t.Parallel()
	b := &contentBuffer{
		styles:        testStyles(theme.AccentAmber),
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

func TestUserSegmentMargin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		segments          []contentSegment
		wantMarginAbove   bool // blank line above first user segment
		wantMarginBelow   bool // blank line below last user segment
		wantMarginBetween bool // blank line between consecutive user segments
	}{
		{
			name: "between two assistants",
			segments: []contentSegment{
				{kind: segmentAssistantMarkdown, text: "hello from assistant one"},
				{kind: segmentUser, text: "user message"},
				{kind: segmentAssistantMarkdown, text: "hello from assistant two"},
			},
			wantMarginAbove: true,
			wantMarginBelow: true,
		},
		{
			name: "user followed by assistant",
			segments: []contentSegment{
				{kind: segmentUserMarkdown, text: "# User Title\ncontent"},
				{kind: segmentAssistantMarkdown, text: "assistant response"},
			},
			wantMarginAbove: false,
			wantMarginBelow: true,
		},
		{
			name: "two consecutive users",
			segments: []contentSegment{
				{kind: segmentUser, text: "first user says hi"},
				{kind: segmentUserMarkdown, text: "# Second user\nwith text"},
			},
			wantMarginAbove:   false,
			wantMarginBelow:   false,
			wantMarginBetween: false,
		},
		{
			name: "single user alone",
			segments: []contentSegment{
				{kind: segmentUser, text: "just me"},
			},
			wantMarginAbove: false,
			wantMarginBelow: false,
		},
		{
			name: "assistant followed by user",
			segments: []contentSegment{
				{kind: segmentAssistantMarkdown, text: "assistant says something"},
				{kind: segmentUser, text: "user reply"},
			},
			wantMarginAbove: true,
			wantMarginBelow: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := &contentBuffer{
				styles:        testStyles(theme.AccentAmber),
				collapseState: make(map[int]bool),
			}
			b.segments = tt.segments
			got := b.String(80)
			lines := strings.Split(got, "\n")

			// Collect indices of lines containing the user bar character.
			var userLineIdxs []int
			for i, line := range lines {
				if strings.Contains(line, "\u2503") {
					userLineIdxs = append(userLineIdxs, i)
				}
			}

			if len(userLineIdxs) == 0 {
				t.Fatal("no user lines (\u2503) found in output")
			}

			firstUser := userLineIdxs[0]
			lastUser := userLineIdxs[len(userLineIdxs)-1]

			// Promote "no ┃" helper for blank-line detection.
			// A background-blank line has lipgloss.Width == 80 and contains no ┃.
			isBlankLine := func(line string) bool {
				return !strings.Contains(line, "\u2503") && (line == "" || lipgloss.Width(line) == 80)
			}

			// Margin above the first user segment.
			if tt.wantMarginAbove {
				if firstUser == 0 {
					t.Errorf("want blank line above user segment, but first line of output contains \u2503")
				} else if !isBlankLine(lines[firstUser-1]) {
					t.Errorf("line before first user line is not a background-blank line: %q", lines[firstUser-1])
				}
			} else {
				if firstUser > 0 {
					t.Errorf("no blank line expected above user, but first user line is at index %d (not first line)", firstUser)
				}
			}

			// Margin below the last user segment.
			if tt.wantMarginBelow {
				if lastUser == len(lines)-1 {
					t.Errorf("want blank line below user segment, but last line of output contains \u2503")
				} else if !isBlankLine(lines[lastUser+1]) {
					t.Errorf("line after last user line is not a background-blank line: %q", lines[lastUser+1])
				}
			} else {
				if lastUser < len(lines)-1 && isBlankLine(lines[lastUser+1]) {
					t.Errorf("no blank line expected below user, but line after last user is a background-blank line")
				}
			}

			// Margin between consecutive user segments.
			foundGap := false
			for i := 1; i < len(userLineIdxs); i++ {
				if userLineIdxs[i] > userLineIdxs[i-1]+1 {
					foundGap = true
					break
				}
			}
			if tt.wantMarginBetween && !foundGap {
				t.Errorf("want blank line between consecutive user segments, but all user lines are contiguous")
			}
			if !tt.wantMarginBetween && foundGap {
				t.Errorf("no blank line expected between consecutive user segments")
			}
		})
	}
}

func TestJoinSeparatorBySegmentKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		kinds []contentSegmentKind
		want  string
	}{
		{
			name:  "same assistant kind hugs",
			kinds: []contentSegmentKind{segmentAssistantMarkdown, segmentAssistantMarkdown},
			want:  "first\nsecond",
		},
		{
			name:  "different kinds have blank line",
			kinds: []contentSegmentKind{segmentAssistantMarkdown, segmentDelegation},
			want:  "first\n\nsecond",
		},
		{
			name:  "user kinds hug",
			kinds: []contentSegmentKind{segmentUser, segmentUserMarkdown},
			want:  "first\nsecond",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := joinWithUserMargin([]string{"first", "second"}, tt.kinds)
			if got != tt.want {
				t.Errorf("joinWithUserMargin(%v) = %q, want %q", tt.kinds, got, tt.want)
			}
		})
	}
}

func TestPluralTurns(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			result := pluralTurns(tt.count)
			if result != tt.want {
				t.Errorf("pluralTurns(%d) = %q, want %q", tt.count, result, tt.want)
			}
		})
	}
}

func TestAppendEventDisplayFileUsesExplicitPreviewDocument(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "read", "call_1", map[string]any{
		"path": "main.go",
	}))
	buffer.AppendEvent(output.NewToolCallFinishedEvent(1, "read", "call_1", `{"path":"main.go","output":"package main\n"}`, nil))

	if len(buffer.segments) != 1 || buffer.segments[0].toolData == nil {
		t.Fatalf("tool segments = %#v, want one populated tool segment", buffer.segments)
	}
	seg := buffer.segments[0].toolData
	if got, want := seg.preview.Kind, output.ToolPreviewKindReadFile; got != want {
		t.Fatalf("preview kind = %q, want %q", got, want)
	}
	if got, want := seg.bodyKind, "file"; got != want {
		t.Fatalf("bodyKind = %q, want %q", got, want)
	}
	if got, want := seg.preview.Path, "main.go"; got != want {
		t.Fatalf("preview path = %q, want %q", got, want)
	}
}

func TestAppendEventScopedChildToolEventDoesNotAppendTopLevelToolSegment(t *testing.T) {
	t.Parallel()
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
	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want delegation state")
	}
	if got := dd.currentOperation; got != "read: README.md:1–200" {
		t.Fatalf("currentOperation = %q, want %q", got, "read: README.md:1–200")
	}
	if got := len(dd.entries); got != 1 {
		t.Fatalf("entries count = %d, want 1", got)
	}
	if got := dd.entries[0].kind; got != delegationTranscriptEntryTool {
		t.Fatalf("entry kind = %v, want delegationTranscriptEntryTool", got)
	}
	if got := dd.entries[0].args; got != "README.md:1–200" {
		t.Fatalf("entry args = %q, want %q", got, "README.md:1–200")
	}
}

func TestAppendEventScopedChildToolEventFallsBackWhenTargetMissing(t *testing.T) {
	t.Parallel()
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

func TestAppendEventDelegateParentToolCallMergesIntoDelegationSegment(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call_delegate_1", map[string]any{
		"task": "fix the bug in module X",
	}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "fix the bug in module X"))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	if got := buffer.segments[0].kind; got != segmentDelegation {
		t.Fatalf("segment kind = %v, want segmentDelegation", got)
	}
	if buffer.segments[0].toolData != nil {
		t.Fatal("toolData != nil, want no top-level tool box for delegate")
	}

	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want delegation state")
	}
	if got := dd.agentID; got != "child-1" {
		t.Fatalf("agentID = %q, want child-1", got)
	}
	if got := dd.parentCallID; got != "call_delegate_1" {
		t.Fatalf("parentCallID = %q, want call_delegate_1", got)
	}
	if got := dd.parentArgs; got != "fix the bug in module X" {
		t.Fatalf("parentArgs = %q, want %q", got, "fix the bug in module X")
	}
	if got := dd.promptText; got != "fix the bug in module X" {
		t.Fatalf("promptText = %q, want canonical full parent task", got)
	}
}

func TestAppendEventDelegationStartedBeforeDelegateParentToolCallMergesIntoOneSegment(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "fix the bug in module X"))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call_delegate_1", map[string]any{
		"task": "fix the bug in module X",
	}))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	if got := buffer.segments[0].kind; got != segmentDelegation {
		t.Fatalf("segment kind = %v, want segmentDelegation", got)
	}

	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want delegation state")
	}
	if got := dd.agentID; got != "child-1" {
		t.Fatalf("agentID = %q, want child-1", got)
	}
	if got := dd.parentCallID; got != "call_delegate_1" {
		t.Fatalf("parentCallID = %q, want call_delegate_1", got)
	}
	if got := dd.parentArgs; got != "fix the bug in module X" {
		t.Fatalf("parentArgs = %q, want %q", got, "fix the bug in module X")
	}
}

func TestAppendEventNormalParentBashToolStillUsesToolCallSegment(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call_bash_1", map[string]any{
		"command": "pwd",
	}))

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
	t.Parallel()
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

func TestAppendEventScopedChildToolFinishedEventUpdatesExistingTranscriptEntry(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "do work"))
	buffer.AppendEvent(output.WithAgentScope(
		output.NewToolCallStartedEvent(1, "read", "call_1", map[string]any{"path": "README.md"}),
		"child-1",
	))
	buffer.segments[0].renderDirty = false

	buffer.AppendEvent(output.WithAgentScope(
		output.NewToolCallFinishedEvent(1, "read", "call_1", "file contents", nil),
		"child-1",
	))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want delegation state")
	}
	if got := len(dd.entries); got != 1 {
		t.Fatalf("entries count = %d, want 1", got)
	}
	entry := dd.entries[0]
	if got := entry.status; got != "complete" {
		t.Fatalf("entry status = %q, want complete", got)
	}
	if got := entry.body; got != "file contents" {
		t.Fatalf("entry body = %q, want %q", got, "file contents")
	}
	if !buffer.segments[0].renderDirty {
		t.Fatal("delegation segment renderDirty = false, want true")
	}
}

func TestAppendEventScopedChildAssistantEventsUpdateTranscriptState(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "do work"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "hello ", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "world", output.ChunkSourceAssistant), "child-1"))

	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want delegation state")
	}
	if got := len(dd.entries); got != 1 {
		t.Fatalf("entries count after chunks = %d, want 1", got)
	}
	if got := dd.entries[0].body; got != "hello world" {
		t.Fatalf("entry body after chunks = %q, want %q", got, "hello world")
	}
	if got := dd.currentOperation; got != "hello world" {
		t.Fatalf("currentOperation after chunks = %q, want %q", got, "hello world")
	}

	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", "follow-up"), "child-1"))

	if got := len(dd.entries); got != 2 {
		t.Fatalf("entries count after final message = %d, want 2", got)
	}
	if got := dd.entries[0].body; got != "hello world" {
		t.Fatalf("first entry body after final message = %q, want %q", got, "hello world")
	}
	if got := dd.entries[1].body; got != "follow-up" {
		t.Fatalf("second entry body after final message = %q, want %q", got, "follow-up")
	}
	if got := dd.currentOperation; got != "follow-up" {
		t.Fatalf("currentOperation after final message = %q, want %q", got, "follow-up")
	}
}

func TestAppendEventScopedChildThinkingEventsStayInsideDelegationTranscript(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		showThinking:  true,
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "do work"))
	buffer.AppendEvent(output.WithAgentScope(output.NewThinkingChunkEventWithSource(1, "plan ", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewThinkingChunkEventWithSource(1, "search", output.ChunkSourceAssistant), "child-1"))

	if got := len(buffer.segments); got != 1 {
		t.Fatalf("segments count = %d, want 1", got)
	}
	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want delegation state")
	}
	if got := len(dd.entries); got != 1 {
		t.Fatalf("entries count = %d, want 1", got)
	}
	if got := dd.entries[0].kind; got != delegationTranscriptEntryThinking {
		t.Fatalf("entry kind = %v, want delegationTranscriptEntryThinking", got)
	}
	if got := dd.entries[0].body; got != "plan search" {
		t.Fatalf("entry body = %q, want %q", got, "plan search")
	}
	if got := dd.currentOperation; got != "plan search" {
		t.Fatalf("currentOperation = %q, want %q", got, "plan search")
	}
}

func TestRenderDelegationExpandedShowsChildThinkingInsideBox(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
		showThinking:  true,
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call_delegate_1", map[string]any{
		"task": "inspect docs",
	}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "inspect docs"))
	buffer.AppendEvent(output.WithAgentScope(output.NewThinkingChunkEventWithSource(1, "inspect files", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", "child assistant reply"), "child-1"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "child-1",
		Status:        "complete",
		TurnCount:     2,
		TokenCount:    25,
		ToolCallCount: 0,
		Output:        "final child output",
	}))
	buffer.ToggleLastDelegationOutput()

	rendered := stripANSI(buffer.String(80))
	for _, want := range []string{"explore", "child-1", "inspect files", "child assistant reply"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expanded delegation render %q missing %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "▾ Thinking") || strings.Contains(rendered, "▸ Thinking") {
		t.Fatalf("delegation child thinking should render inline, not as a top-level thinking block: %q", rendered)
	}
}

func TestAppendEventScopedChildAssistantDuplicateFinalMessageSuppressed(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "do work"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "hello", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, " world", output.ChunkSourceAssistant), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", "hello world"), "child-1"))

	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil, want delegation state")
	}
	if got := len(dd.entries); got != 1 {
		t.Fatalf("entries count = %d, want 1", got)
	}
	if got := dd.entries[0].body; got != "hello world" {
		t.Fatalf("entry body = %q, want %q", got, "hello world")
	}
}

func TestAppendEventScopedChildEventsRouteByAgentID(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "do work"))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-2", "other work"))
	buffer.AppendEvent(output.WithAgentScope(
		output.NewToolCallStartedEvent(1, "read", "call_1", map[string]any{"path": "README.md"}),
		"child-1",
	))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "second child answer", output.ChunkSourceAssistant), "child-2"))

	if buffer.segments[0].kind != segmentDelegationGroup {
		t.Fatalf("segment 0 kind = %v, want segmentDelegationGroup (consecutive delegations merge)", buffer.segments[0].kind)
	}
	group := buffer.segments[0].delegGroupData
	if group == nil || len(group.entries) != 2 {
		t.Fatalf("group has %d entries, want 2", len(group.entries))
	}
	first := group.entries[0]
	second := group.entries[1]
	if got := len(first.entries); got != 1 {
		t.Fatalf("child-1 entries count = %d, want 1", got)
	}
	if got := first.entries[0].kind; got != delegationTranscriptEntryTool {
		t.Fatalf("child-1 first entry kind = %v, want delegationTranscriptEntryTool", got)
	}
	if got := len(second.entries); got != 1 {
		t.Fatalf("child-2 entries count = %d, want 1", got)
	}
	if got := second.entries[0].kind; got != delegationTranscriptEntryAssistant {
		t.Fatalf("child-2 first entry kind = %v, want delegationTranscriptEntryAssistant", got)
	}
	if got := second.currentOperation; got != "second child answer" {
		t.Fatalf("child-2 currentOperation = %q, want %q", got, "second child answer")
	}
}

func TestRenderToolPreviewUsesStructuredFilePreview(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		styles:        testStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}
	buffer.segments = []contentSegment{
		{
			kind: segmentToolCall,
			toolData: &toolCallSegment{
				tool:      "read",
				args:      "notes.md",
				bodyKind:  "file",
				collapsed: false,
				preview: output.ToolPreview{
					Kind:     output.ToolPreviewKindReadFile,
					Path:     "notes.md",
					Contents: "hello\nworld\n",
				},
			},
		},
	}

	got := buffer.String(80)
	for _, want := range []string{"read file preview", "notes.md", "hello", "world"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered preview %q missing %q", got, want)
		}
	}
}

func TestToolBorderStyleUsesMutedPalette(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		styles: testStyles(theme.AccentAmber),
	}

	tests := []struct {
		name string
		tool string
		want color.Color
	}{
		{name: "bash", tool: "bash", want: lipgloss.Color(theme.ToolAmberLine)},
		{name: "read", tool: "read", want: lipgloss.Color(theme.ToolCyanLine)},
		{name: "mutate", tool: "mutate", want: lipgloss.Color(theme.ToolGrnLine)},
		{name: "grep", tool: "grep", want: lipgloss.Color(theme.ToolMagLine)},
		{name: "glob", tool: "glob", want: lipgloss.Color(theme.ToolBlueLine)},
		{name: "fetch_url", tool: "fetch_url", want: lipgloss.Color(theme.ToolBlueLine)},
		{name: "todo", tool: "todo", want: lipgloss.Color(theme.WarnLine)},
		{name: "default", tool: "ls", want: lipgloss.Color(theme.AccentLine)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buffer.toolBorderStyle(tt.tool).GetForeground()
			if got != tt.want {
				t.Fatalf("toolBorderStyle(%q) foreground = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

func TestRenderAdjacentSameToolCallsAsOneGroupedBox(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		styles:        testStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "pwd"}))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call_2", map[string]any{"command": "git status"}))

	if got := len(buffer.segments); got != 1 {
		t.Fatalf("segments = %d, want 1 grouped segment", got)
	}
	if got := buffer.segments[0].kind; got != segmentToolCallGroup {
		t.Fatalf("segment kind = %v, want segmentToolCallGroup", got)
	}
	group := buffer.segments[0].toolGroupData
	if group == nil {
		t.Fatal("toolGroupData = nil, want grouped tool calls")
	}
	if got := len(group.entries); got != 2 {
		t.Fatalf("group entries = %d, want 2", got)
	}

	rendered := stripANSI(buffer.String(140))
	if !strings.Contains(rendered, "pwd") || !strings.Contains(rendered, "git status") {
		t.Fatalf("rendered grouped tool output %q missing commands", rendered)
	}
	if got := strings.Count(rendered, "┌"); got != 1 {
		t.Fatalf("group top borders = %d, want 1", got)
	}
	if got := strings.Count(rendered, "└"); got != 1 {
		t.Fatalf("group bottom borders = %d, want 1", got)
	}
}

func TestRenderSingleBashToolCallStaysBoxed(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		styles:        testStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "pwd"}))

	rendered := stripANSI(buffer.String(80))
	if got := strings.Count(rendered, "┌"); got != 1 {
		t.Fatalf("single tool top borders = %d, want 1", got)
	}
	if got := strings.Count(rendered, "└"); got != 1 {
		t.Fatalf("single tool bottom borders = %d, want 1", got)
	}
}

func TestRenderMixedAdjacentToolsAsOneGroupedBox(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		styles:        testStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "pwd"}))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "read", "call_2", map[string]any{"path": "README.md"}))

	if got := len(buffer.segments); got != 1 {
		t.Fatalf("segments = %d, want 1 grouped segment", got)
	}
	group := buffer.segments[0].toolGroupData
	if group == nil || !group.mixed {
		t.Fatalf("group = %#v, want mixed group", group)
	}
	rendered := stripANSI(buffer.String(140))
	if got := strings.Count(rendered, "┌"); got != 1 {
		t.Fatalf("mixed top borders = %d, want 1", got)
	}
}

func TestRenderNonToolSegmentBreaksToolGrouping(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		styles:        testStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "pwd"}))
	buffer.AppendLine("note")
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call_2", map[string]any{"command": "git status"}))

	if got := len(buffer.segments); got != 3 {
		t.Fatalf("segments = %d, want 3 with a non-tool separator", got)
	}
	rendered := stripANSI(buffer.String(100))
	if got := strings.Count(rendered, "┌"); got != 2 {
		t.Fatalf("separated top borders = %d, want 2", got)
	}
}

func TestFinishGroupedToolCallUpdatesMatchingCallID(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		styles:        testStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "pwd"}))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call_2", map[string]any{"command": "git status"}))
	buffer.AppendEvent(output.NewToolCallFinishedEvent(1, "bash", "call_2", "bad", errors.New("boom")))
	buffer.AppendEvent(output.NewToolCallFinishedEvent(1, "bash", "call_1", "ok", nil))

	group := buffer.segments[0].toolGroupData
	if group == nil {
		t.Fatal("toolGroupData = nil, want grouped tool calls")
	}
	if got := group.entries[0].body; got != "ok" {
		t.Fatalf("first grouped call body = %q, want ok", got)
	}
	if got := group.entries[0].meta; got != "✓" {
		t.Fatalf("first grouped call meta = %q, want ✓", got)
	}
	if group.entries[0].hasError {
		t.Fatal("first grouped call hasError = true, want false")
	}
	if got := group.entries[1].body; got != "bad" {
		t.Fatalf("second grouped call body = %q, want bad", got)
	}
	if got := group.entries[1].meta; got != "✗" {
		t.Fatalf("second grouped call meta = %q, want ✗", got)
	}
	if !group.entries[1].hasError {
		t.Fatal("second grouped call hasError = false, want true")
	}
}

func TestRenderToolPreviewUsesChromaStylesForMarkdown(t *testing.T) {
	useTrueColor(t)

	buffer := &contentBuffer{
		styles:        testStyles(theme.AccentAmber),
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

func TestRenderToolPreviewUsesChromaStylesForMakefile(t *testing.T) {
	useTrueColor(t)

	buffer := &contentBuffer{
		styles:        testStyles(theme.AccentAmber),
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
	t.Parallel()
	preview := output.FormatFilePreviewWithLimit("snippet.go", `package main
func main() {}
`, 10)
	if len(preview.Lines) == 0 || !lineHasHighlightedSpan(preview.Lines[0]) {
		t.Fatal("preview document is not syntax-highlighted")
	}
	buffer := &contentBuffer{
		styles:        testStyles(theme.AccentAmber),
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
	plain := stripANSI(got)
	for _, want := range []string{"▾", "display file preview", "snippet.go", "package main", "func main()"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered display preview %q missing %q", got, want)
		}
	}
}

func TestRenderReadFilePreviewIncludesLanguageInCaption(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		styles:        testStyles(theme.AccentAmber),
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
		styles:        testStyles(theme.AccentAmber),
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

func TestRenderToolPreviewPreservesDiffSyntaxHighlighting(t *testing.T) {
	useTrueColor(t)

	buffer := &contentBuffer{
		styles:        testStyles(theme.AccentAmber),
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
		styles:        testStyles(theme.AccentAmber),
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

func TestRenderToolPreviewTruncatesLongFileBodies(t *testing.T) {
	t.Parallel()
	var body strings.Builder
	for i := 0; i < 30; i++ {
		body.WriteString("line\n")
	}

	buffer := &contentBuffer{
		styles:        testStyles(theme.AccentAmber),
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
	t.Parallel()
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
			t.Parallel()
			buffer := &contentBuffer{
				styles:        testStyles(theme.AccentAmber),
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
			plain := stripANSI(got)
			for _, want := range tt.want {
				if !strings.Contains(plain, want) {
					t.Fatalf("rendered preview %q missing %q", got, want)
				}
			}
		})
	}
}

func TestRenderToolPreviewUsesStructuredGrepViews(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			buffer := &contentBuffer{
				styles:        testStyles(theme.AccentAmber),
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
			plain := stripANSI(got)
			for _, want := range tt.want {
				if !strings.Contains(plain, want) {
					t.Fatalf("rendered preview %q missing %q", got, want)
				}
			}
		})
	}
}

func TestRenderToolPreviewUsesStructuredBashView(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			buffer := &contentBuffer{
				styles:        testStyles(theme.AccentAmber),
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

			got := stripANSI(buffer.String(100))
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("rendered preview %q missing %q", got, want)
				}
			}
		})
	}
}

func TestIsSpecializedDelegateTool(t *testing.T) {
	t.Parallel()
	tests := []struct {
		tool string
		want bool
	}{
		{"explore", true},
		{"research", true},
		{"code", true},
		{"evaluate", true},
		{"sanity_check", true},
		{"review", true},
		// case-insensitive
		{"Explore", true},
		{"RESEARCH", true},
		{"Code", true},
		// whitespace trimmed
		{" evaluate ", true},
		// non-specialized tools
		{"delegate", false},
		{"bash", false},
		{"read", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isSpecializedDelegateTool(tt.tool); got != tt.want {
			t.Errorf("isSpecializedDelegateTool(%q) = %v, want %v", tt.tool, got, tt.want)
		}
	}
}

func TestSummarizeArgsSpecializedDelegateTools(t *testing.T) {
	t.Parallel()
	tests := []struct {
		tool string
		args map[string]any
		want string
	}{
		{
			tool: "explore",
			args: map[string]any{"task": "look into the auth module"},
			want: "look into the auth module",
		},
		{
			tool: "research",
			args: map[string]any{"task": "find all usages of X"},
			want: "find all usages of X",
		},
		{
			tool: "code",
			args: map[string]any{"task": "implement the retry logic"},
			want: "implement the retry logic",
		},
		{
			tool: "evaluate",
			args: map[string]any{"task": "plan the migration"},
			want: "plan the migration",
		},
		{
			tool: "sanity_check",
			args: map[string]any{"task": "run all tests and confirm green"},
			want: "run all tests and confirm green",
		},
		{
			tool: "mutate",
			args: map[string]any{"operations": []any{
				map[string]any{"type": "replace", "path": "internal/tool/builtin/mutate.go"},
				map[string]any{"type": "move", "from": "old.go", "to": "new.go"},
			}},
			want: "replace internal/tool/builtin/mutate.go (+1 more)",
		},
	}
	for _, tt := range tests {
		got := summarizeArgs(tt.tool, tt.args)
		if got != tt.want {
			t.Errorf("summarizeArgs(%q, ...) = %q, want %q", tt.tool, got, tt.want)
		}
	}
}

func TestSpecializedDelegateToolCallCreatesSingleDelegationSegment(t *testing.T) {
	t.Parallel()
	tools := []string{"explore", "research", "code", "evaluate", "sanity_check", "review"}
	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			buffer := &contentBuffer{segments: make([]contentSegment, 0)}
			buffer.AppendEvent(output.NewToolCallStartedEvent(1, tool, "call-1", map[string]any{"task": "do something"}))

			if len(buffer.segments) != 1 {
				t.Fatalf("segments count = %d, want 1 (single delegation box)", len(buffer.segments))
			}
			seg := buffer.segments[0]
			if seg.kind != segmentDelegation {
				t.Errorf("segment kind = %v, want segmentDelegation", seg.kind)
			}
			if seg.delegData == nil {
				t.Fatal("delegData = nil, want delegation state")
			}
			if seg.delegData.toolLabel != tool {
				t.Errorf("toolLabel = %q, want %q", seg.delegData.toolLabel, tool)
			}
		})
	}
}

func TestDelegationHeaderRendersSpecializedToolLabel(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		styles:        testStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call-1", map[string]any{"task": "map the codebase"}))

	if len(buffer.segments) == 0 || buffer.segments[0].delegData == nil {
		t.Fatal("expected delegation segment")
	}
	dd := buffer.segments[0].delegData
	header := buffer.renderDelegationHeader(dd, 80)

	if !strings.Contains(header, "explore") {
		t.Errorf("delegation header %q missing tool label %q", header, "explore")
	}
	if strings.Contains(header, "delegate") {
		t.Errorf("delegation header %q must not show %q for specialized tool", header, "delegate")
	}
}

func TestDelegationHeaderLabelHasNoGenericDelegateFallback(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		styles:        testStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "delegate", "call-2", map[string]any{"task": "do work"}))

	if len(buffer.segments) == 0 {
		t.Fatal("expected segment")
	}
	if buffer.segments[0].kind != segmentToolCall {
		t.Errorf("segment kind = %v, want segmentToolCall (generic delegate is not delegation-capable)", buffer.segments[0].kind)
	}
	if buffer.segments[0].delegData != nil {
		t.Fatal("delegData != nil, want no delegation state for non-specialized tool")
	}
}

func TestRenderReplayDelegationUsesParentToolStartForPromptAndOutput(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}

	jsonBlob := `{"agent_id":"agent-evaluate-1","status":"complete","output":"final prose output","tool_call_count":2}`
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "evaluate", "call_evaluate_1", map[string]any{
		"task": "plan the rollout\nwith full prompt text",
	}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("agent-evaluate-1", "plan the rollout\nwith full prompt text"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "agent-evaluate-1",
		Status:        "complete",
		TurnCount:     3,
		TokenCount:    456,
		ToolCallCount: 2,
		Output:        "final prose output",
	}))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	dd := buffer.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil")
	}
	if got, want := dd.toolLabel, "evaluate"; got != want {
		t.Fatalf("toolLabel = %q, want %q", got, want)
	}
	if got, want := dd.promptText, "plan the rollout\nwith full prompt text"; got != want {
		t.Fatalf("promptText = %q, want %q", got, want)
	}
	if !dd.promptCollapsed {
		t.Fatal("promptCollapsed = false, want true")
	}

	buffer.ToggleLastDelegationOutput()
	dd.promptCollapsed = false
	buffer.segments[0].renderDirty = true

	rendered := stripANSI(buffer.String(80))
	for _, want := range []string{"evaluate", "▾ prompt", "plan the rollout", "with full prompt text", "output", "final prose output"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("replay delegation render %q missing %q", rendered, want)
		}
	}
	if strings.Contains(rendered, `"agent_id"`) || strings.Contains(rendered, `"output"`) || strings.Contains(rendered, jsonBlob) {
		t.Fatalf("replay delegation render leaked serialized json: %q", rendered)
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

func TestDelegationStatsFooterVisibleWhenExpandedComplete(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call_1", map[string]any{"task": "do work"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("agent-1", "do work"))
	buffer.AppendEvent(output.WithAgentScope(output.NewModelCallStartedEvent(1, "qwen3-coder-30b", 12), "agent-1"))
	buffer.AppendEvent(output.WithAgentScope(
		output.NewContextTokenBudgetEvent("", 1, 2400, 2400, 200000, 1.2, 90.0, 0, 0, "", false),
		"agent-1",
	))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", "working on it"), "agent-1"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:           "agent-1",
		Status:            "complete",
		TurnCount:         5,
		TokenCount:        1234,
		ToolCallCount:     3,
		Output:            "result",
		InputTokens:       50,
		CacheReadTokens:   900,
		CacheCreateTokens: 50,
	}))
	buffer.ToggleLastDelegationOutput()

	rendered := stripANSI(buffer.String(100))
	normalized := strings.Join(strings.Fields(rendered), " ")
	for _, want := range []string{"model qwen3-coder-", "30b", "Turns: 5", "Tool Calls: 3", "Tokens: 1.0k in / 1.2k out", "Duration:", "Ctx: 1%"} {
		if !strings.Contains(normalized, want) {
			t.Errorf("expanded delegation render missing %q:\n%s", want, rendered)
		}
	}
	if !strings.Contains(normalized, "Status:") || !strings.Contains(normalized, "complete") {
		t.Errorf("expanded delegation render missing status text:\n%s", rendered)
	}
	if !strings.Contains(rendered, "2.4k / 200k") {
		t.Errorf("expanded delegation render missing context capacity:\n%s", rendered)
	}
	if !strings.Contains(rendered, "────────────────") {
		t.Errorf("expanded delegation render missing footer separator:\n%s", rendered)
	}
}

func TestDelegationStatsFooterHiddenWhenCollapsed(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call_1", map[string]any{"task": "do work"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("agent-1", "do work"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "agent-1",
		Status:        "complete",
		TurnCount:     5,
		TokenCount:    1234,
		ToolCallCount: 0,
		Output:        "result",
	}))

	// collapsed (default) — stats footer must not appear
	rendered := stripANSI(buffer.String(100))
	for _, unexpected := range []string{"Turns: 5", "Tokens: 1234", "Status: complete"} {
		if strings.Contains(rendered, unexpected) {
			t.Errorf("collapsed delegation render should not contain %q:\n%s", unexpected, rendered)
		}
	}
}

func TestDelegationStatsFooterVisibleWhenExpandedActive(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "plan", "call_1", map[string]any{"task": "plan work"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("agent-1", "plan work"))
	buffer.AppendEvent(output.WithAgentScope(
		output.NewAPIRequestEvent("deepseek-v4-flash", nil, nil, nil, nil, prompt.ModelTokenBudget{}, 0, 0),
		"agent-1",
	))
	buffer.AppendEvent(output.WithAgentScope(
		output.NewContextTokenBudgetEvent("", 1, 80000, 80000, 200000, 42.5, 90.0, 0, 0, "", false),
		"agent-1",
	))
	buffer.ToggleLastDelegationOutput()

	rendered := stripANSI(buffer.String(120))
	for _, want := range []string{"model deepseek-v4-flash", "Duration:", "Status: active", "Ctx: 40% (80k / 200k)"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("expanded active delegation render missing %q:\n%s", want, rendered)
		}
	}
}

func TestDelegationExtensionCounter(t *testing.T) {
	t.Parallel()
	t.Run("newly started delegation shows initial capacity", func(t *testing.T) {
		t.Parallel()
		buffer := &contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        testStyles(theme.AccentAmber),
		}

		buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call_1", map[string]any{"task": "do work"}))
		buffer.AppendEvent(output.NewDelegationStartedEvent("agent-1", "do work"))
		buffer.ToggleLastDelegationOutput()

		rendered := stripANSI(buffer.String(100))
		if !strings.Contains(rendered, "Extension: 0/5") {
			t.Fatalf("expanded delegation render missing initial extension capacity:\n%s", rendered)
		}
	})

	t.Run("extension events update same segment without raw status line", func(t *testing.T) {
		t.Parallel()
		buffer := &contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        testStyles(theme.AccentAmber),
		}

		buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call_1", map[string]any{"task": "do work"}))
		buffer.AppendEvent(output.NewDelegationStartedEvent("agent-1", "do work"))
		buffer.AppendEvent(output.NewDelegationExtensionEvent("agent-1", 1, 5))
		buffer.AppendEvent(output.NewDelegationExtensionEvent("agent-1", 2, 5))
		buffer.ToggleLastDelegationOutput()

		if len(buffer.segments) != 1 {
			t.Fatalf("segments count = %d, want 1", len(buffer.segments))
		}
		dd := buffer.segments[0].delegData
		if dd == nil {
			t.Fatal("delegData = nil, want delegation state")
		}
		if dd.extCurrent != 2 || dd.extMax != 5 {
			t.Fatalf("extension state = %d of %d, want 2 of 5", dd.extCurrent, dd.extMax)
		}

		rendered := stripANSI(buffer.String(100))
		if !strings.Contains(rendered, "Extension: 2/5") {
			t.Fatalf("expanded delegation render missing updated extension count:\n%s", rendered)
		}
		for _, unexpected := range []string{"Extension: 1/5", "status · delegation_extension", "delegation_extension"} {
			if strings.Contains(rendered, unexpected) {
				t.Fatalf("expanded delegation render contains unexpected %q:\n%s", unexpected, rendered)
			}
		}
	})

	t.Run("extension appears after context", func(t *testing.T) {
		t.Parallel()
		buffer := &contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        testStyles(theme.AccentAmber),
		}

		buffer.AppendEvent(output.NewDelegationStartedEvent("agent-1", "do work"))
		buffer.AppendEvent(output.WithAgentScope(
			output.NewContextTokenBudgetEvent("", 1, 2400, 2400, 200000, 1.2, 90.0, 0, 0, "", false),
			"agent-1",
		))
		buffer.AppendEvent(output.NewDelegationExtensionEvent("agent-1", 1, 5))
		buffer.ToggleLastDelegationOutput()

		rendered := stripANSI(buffer.String(100))
		ctxIndex := strings.Index(rendered, "Ctx: 1%")
		extIndex := strings.Index(rendered, "Extension: 1/5")
		if ctxIndex < 0 || extIndex < 0 || extIndex < ctxIndex {
			t.Fatalf("extension footer should appear after context:\n%s", rendered)
		}
	})

	t.Run("unknown agent extension is ignored", func(t *testing.T) {
		t.Parallel()
		buffer := &contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        testStyles(theme.AccentAmber),
		}

		buffer.AppendEvent(output.NewDelegationExtensionEvent("missing-agent", 1, 5))

		if len(buffer.segments) != 0 {
			t.Fatalf("segments count = %d, want 0", len(buffer.segments))
		}
	})
}

func TestBuildMutateLinesRendersOperations(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		styles: testStyles(theme.AccentAmber),
	}

	tc := &toolCallSegment{
		tool:     "mutate",
		bodyKind: "mutate",
		preview: output.ToolPreview{
			Kind:         output.ToolPreviewKindMutate,
			HunksApplied: 3,
			HunksFailed:  1,
			MutateOperations: []output.ToolPreviewMutateOperation{
				{Type: "create", Path: "new.go", Content: "package main\n"},
				{Type: "replace", Path: "existing.go", OldString: "old", NewString: "new"},
				{Type: "delete_file", Path: "old.go"},
				{Type: "move", From: "a.go", To: "b.go"},
			},
		},
	}

	lines := buffer.buildMutateLines(tc, 80)
	got := stripANSI(strings.Join(lines, "\n"))

	// Summary
	if !strings.Contains(got, "4 operations") {
		t.Fatalf("missing operation count summary: %q", got)
	}
	if !strings.Contains(got, "3 applied") {
		t.Fatalf("missing applied count: %q", got)
	}
	if !strings.Contains(got, "1 failed") {
		t.Fatalf("missing failed count: %q", got)
	}

	// Badges and paths
	if !strings.Contains(got, "new.go") {
		t.Fatalf("missing create path: %q", got)
	}
	if !strings.Contains(got, "existing.go") {
		t.Fatalf("missing replace path: %q", got)
	}
	if !strings.Contains(got, "old.go") {
		t.Fatalf("missing delete_file path: %q", got)
	}
	if !strings.Contains(got, "a.go") || !strings.Contains(got, "b.go") {
		t.Fatalf("missing move paths: %q", got)
	}

	// Create should have syntax-highlighted content (package main)
	if !strings.Contains(got, "package main") {
		t.Fatalf("missing create file content: %q", got)
	}

	// Replace should show diff lines
	if !strings.Contains(got, "- old") {
		t.Fatalf("missing replace old line: %q", got)
	}
	if !strings.Contains(got, "+ new") {
		t.Fatalf("missing replace new line: %q", got)
	}

	// delete_file should show D badge and path
	// move should NOT have content block
}

func TestBuildMutateLinesFallsBackToPlainWhenEmptyOperations(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		styles: testStyles(theme.AccentAmber),
	}

	tc := &toolCallSegment{
		tool:     "mutate",
		body:     `{"message":"ok"}`,
		bodyKind: "mutate",
		preview: output.ToolPreview{
			Kind:             output.ToolPreviewKindMutate,
			MutateOperations: nil,
		},
	}

	lines := buffer.buildMutateLines(tc, 80)
	got := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(got, `{"message":"ok"}`) {
		t.Fatalf("expected plain JSON fallback, got: %q", got)
	}
}

func TestBuildMutateLinesWriteShowsModifiedBadge(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		styles: testStyles(theme.AccentAmber),
	}

	tc := &toolCallSegment{
		tool:     "mutate",
		bodyKind: "mutate",
		preview: output.ToolPreview{
			Kind: output.ToolPreviewKindMutate,
			MutateOperations: []output.ToolPreviewMutateOperation{
				{Type: "write", Path: "file.go", Content: "package foo\n"},
			},
		},
	}

	lines := buffer.buildMutateLines(tc, 80)
	got := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(got, "package foo") {
		t.Fatalf("missing write file content: %q", got)
	}
	if !strings.Contains(got, "file.go") {
		t.Fatalf("missing write path: %q", got)
	}
}

func TestClearApprovalState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		segments []contentSegment
	}{
		{
			name: "standalone tool call clears approval state",
			segments: []contentSegment{
				{
					kind: segmentToolCall,
					toolData: &toolCallSegment{
						approvalPending:  true,
						approvalResolved: false,
					},
				},
			},
		},
		{
			name: "tool call group clears all entries",
			segments: []contentSegment{
				{
					kind: segmentToolCallGroup,
					toolGroupData: &toolCallGroupSegment{
						tool: "bash",
						entries: []*toolCallSegment{
							{approvalPending: true, approvalResolved: false},
							{approvalPending: true, approvalResolved: false},
						},
					},
				},
			},
		},
		{
			name: "mixed segments only tool calls affected",
			segments: []contentSegment{
				{
					kind: segmentToolCall,
					toolData: &toolCallSegment{
						approvalPending:  true,
						approvalResolved: false,
					},
				},
				{
					kind:      segmentThinkingBlock,
					thinkData: &thinkingBlockData{collapsed: true},
				},
				{
					kind: segmentPlain,
					text: "some text",
				},
			},
		},
		{
			name: "nil toolData does not panic",
			segments: []contentSegment{
				{
					kind:     segmentToolCall,
					toolData: nil,
				},
			},
		},
		{
			name: "nil toolGroupData does not panic",
			segments: []contentSegment{
				{
					kind:          segmentToolCallGroup,
					toolGroupData: nil,
				},
			},
		},
		{
			name:     "empty segments does not panic",
			segments: []contentSegment{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := &contentBuffer{
				segments: tt.segments,
			}
			b.clearApprovalState()

			for i, seg := range b.segments {
				switch seg.kind {
				case segmentToolCall:
					if seg.toolData != nil {
						if seg.toolData.approvalPending {
							t.Errorf("segment[%d] approvalPending = true, want false", i)
						}
						if seg.toolData.approvalResolved {
							t.Errorf("segment[%d] approvalResolved = true, want false", i)
						}
					}
				case segmentToolCallGroup:
					if seg.toolGroupData != nil {
						for j, entry := range seg.toolGroupData.entries {
							if entry != nil {
								if entry.approvalPending {
									t.Errorf("segment[%d] entry[%d] approvalPending = true, want false", i, j)
								}
								if entry.approvalResolved {
									t.Errorf("segment[%d] entry[%d] approvalResolved = true, want false", i, j)
								}
							}
						}
					}
				}
			}
		})
	}
}

func TestApprovalRequestedTargetsGroupedToolCall(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{
		collapseState: make(map[int]bool),
	}

	// Standalone display_file segment (should NOT get approval).
	b.segments = append(b.segments, contentSegment{
		kind: segmentToolCall,
		toolData: &toolCallSegment{
			tool:      "display_file",
			args:      "main.go",
			collapsed: false,
		},
	})

	// Grouped mutate segment with two entries (second should get approval).
	b.segments = append(b.segments, contentSegment{
		kind: segmentToolCallGroup,
		toolGroupData: &toolCallGroupSegment{
			tool: "mutate",
			entries: []*toolCallSegment{
				{tool: "mutate", args: "replace foo.go", meta: "✓", collapsed: true},
				{tool: "mutate", args: "create bar.go", collapsed: true},
			},
		},
	})

	// Fire approval event for mutate.
	b.appendApprovalRequestedEvent(output.Event{
		Type: output.EventTypeApprovalRequested,
		Payload: output.ApprovalEvent{
			Tool:    "mutate",
			Mode:    "prompt",
			Preview: `{"path":"bar.go"}`,
		},
	})

	// display_file must NOT have approval.
	if b.segments[0].toolData.approvalPending {
		t.Fatal("display_file segment got approvalPending, want false")
	}

	// Second entry in the mutate group must have approval.
	group := b.segments[1].toolGroupData
	if group == nil {
		t.Fatal("expected toolGroupData on segment[1]")
	}
	if group.entries[0].approvalPending {
		t.Error("first mutate entry got approvalPending, want false")
	}
	if !group.entries[1].approvalPending {
		t.Fatal("second mutate entry missing approvalPending")
	}
	if group.entries[1].approvalMode != "prompt" {
		t.Errorf("approvalMode = %q, want %q", group.entries[1].approvalMode, "prompt")
	}
	if group.entries[1].collapsed {
		t.Error("approved entry should be uncollapsed")
	}
}

func TestApprovalDecisionTargetsGroupedToolCall(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{
		collapseState: make(map[int]bool),
	}

	// Standalone display_file.
	b.segments = append(b.segments, contentSegment{
		kind: segmentToolCall,
		toolData: &toolCallSegment{
			tool: "display_file",
		},
	})

	// Grouped mutate with second entry pending approval.
	b.segments = append(b.segments, contentSegment{
		kind: segmentToolCallGroup,
		toolGroupData: &toolCallGroupSegment{
			tool: "mutate",
			entries: []*toolCallSegment{
				{tool: "mutate", meta: "✓"},
				{tool: "mutate", approvalPending: true},
			},
		},
	})

	// Fire acceptance event.
	b.appendApprovalDecisionEvent(output.Event{
		Type: output.EventTypeApprovalAccepted,
		Payload: output.ApprovalEvent{
			Tool:    "mutate",
			Allowed: true,
		},
	})

	if b.segments[0].toolData.approvalResolved {
		t.Fatal("display_file should not be resolved")
	}
	entry := b.segments[1].toolGroupData.entries[1]
	if !entry.approvalResolved {
		t.Fatal("second mutate entry should be resolved")
	}
	if !entry.approvalAccepted {
		t.Fatal("second mutate entry should be accepted")
	}
}

func TestRenderToolApprovalBlockMCPShowsServerToolAndSessionButton(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}
	buffer.segments = append(buffer.segments, contentSegment{
		kind: segmentToolCall,
		toolData: &toolCallSegment{
			tool:            "mcp__fixture__echo",
			collapsed:       false,
			approvalPending: true,
			approvalKind:    "mcp",
			approvalServer:  "fixture",
			approvalMCPTool: "echo",
			approvalPreview: `{"message":"hi"}`,
		},
	})

	rendered := stripANSI(buffer.String(100))
	for _, want := range []string{"fixture → echo", "Allowed for session", "Message: hi"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("content = %q, missing %q", rendered, want)
		}
	}
}

func TestContentBufferSegmentHeights(t *testing.T) {
	tests := []struct {
		name     string
		segments []contentSegment
		checks   map[int]int // segment index → expected height; -1 means filtered (not in output)
	}{
		{
			name: "collapsed tool call",
			segments: []contentSegment{
				{
					kind: segmentToolCall,
					toolData: &toolCallSegment{
						tool:      "bash",
						args:      "git status",
						collapsed: true,
					},
				},
			},
			checks: map[int]int{0: 3}, // top border, content line, bottom border
		},
		{
			name: "expanded tool call with body",
			segments: []contentSegment{
				{
					kind: segmentToolCall,
					toolData: &toolCallSegment{
						tool:      "bash",
						args:      "echo hello",
						collapsed: false,
						body:      "Hello world",
						bodyKind:  "plain",
					},
				},
			},
			checks: map[int]int{0: 4}, // top border, header, body, bottom border
		},
		{
			name: "collapsed thinking block",
			segments: []contentSegment{
				{
					kind: segmentThinkingBlock,
					thinkData: &thinkingBlockData{
						collapsed: true,
						body:      "short thought",
					},
				},
			},
			checks: map[int]int{0: 1}, // one collapsed content line
		},
		{
			name: "expanded thinking block",
			segments: []contentSegment{
				{
					kind: segmentThinkingBlock,
					thinkData: &thinkingBlockData{
						collapsed: false,
						body:      "short thought",
					},
				},
			},
			checks: map[int]int{0: 1}, // one expanded content line
		},
		{
			name: "adjacent thinking block and tool call",
			segments: []contentSegment{
				{
					kind: segmentThinkingBlock,
					thinkData: &thinkingBlockData{
						collapsed: true,
						body:      "short",
					},
				},
				{
					kind: segmentToolCall,
					toolData: &toolCallSegment{
						tool:      "bash",
						args:      "git status",
						collapsed: true,
					},
				},
			},
			checks: map[int]int{0: 1, 1: 3}, // thinking=1, tool=3; blank line between not attributed
		},
		{
			name: "empty rendered segment filtered from output",
			segments: []contentSegment{
				{
					kind: segmentApproval,
					text: "",
				},
				{
					kind: segmentPlain,
					text: "visible text",
				},
			},
			checks: map[int]int{0: 1, 1: 1}, // both have lines; approval with empty text is just newline
		},
	}

	useTrueColor(t)
	styles := testStyles(theme.AccentAmber)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &contentBuffer{
				styles:        styles,
				segments:      tt.segments,
				collapseState: make(map[int]bool),
				showThinking:  true,
			}

			_ = b.String(80)

			if len(b.segmentHeights) != len(b.segments) {
				t.Fatalf("segmentHeights length = %d, want %d", len(b.segmentHeights), len(b.segments))
			}

			for idx, want := range tt.checks {
				if want == -1 {
					if b.segmentHeights[idx] != 0 {
						t.Errorf("segmentHeights[%d] = %d, want 0 (filtered)", idx, b.segmentHeights[idx])
					}
				} else if b.segmentHeights[idx] != want {
					t.Errorf("segmentHeights[%d] = %d, want %d", idx, b.segmentHeights[idx], want)
					t.Logf("rendered output:\n%s", b.String(80))
				}
			}
		})
	}
}
func TestFollowUpToolCallCreatesDelegationSegmentWithMatchedLabel(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	// First create an original delegation with "code" label
	b.AppendEvent(output.Event{
		Type: output.EventTypeToolCallStarted,
		Payload: output.ToolCallStartedEvent{
			CallID:    "parent-1",
			Tool:      "code",
			Arguments: map[string]any{"task": "implement feature"},
		},
	})

	// Then trigger DelegationStarted to bind the child agent
	b.AppendEvent(output.Event{
		Type: output.EventTypeDelegationStarted,
		Payload: output.DelegationStartedEvent{
			AgentID:     "child-1",
			TaskPreview: "implement feature",
		},
	})

	// Now simulate a follow_up tool call with agent_id referring to child-1
	b.AppendEvent(output.Event{
		Type: output.EventTypeToolCallStarted,
		Payload: output.ToolCallStartedEvent{
			CallID: "parent-2",
			Tool:   "follow_up",
			Arguments: map[string]any{
				"agent_id": "child-1",
				"message":  "add error handling",
			},
		},
	})

	// Verify delegation group exists (consecutive delegations merge)
	if len(b.segments) < 1 {
		t.Fatalf("got %d segments, want at least 1", len(b.segments))
	}

	// Verify the merged group contains two delegations
	if b.segments[0].kind != segmentDelegationGroup {
		t.Fatalf("segments[0].kind = %v, want segmentDelegationGroup", b.segments[0].kind)
	}

	group := b.segments[0].delegGroupData
	if group == nil || len(group.entries) != 2 {
		t.Fatalf("group has %d entries, want 2", len(group.entries))
	}

	dd := group.entries[1]
	if dd == nil {
		t.Fatal("group.entries[1] is nil")
	}

	// Verify it's marked as follow_up
	if !dd.isFollowUp {
		t.Errorf("isFollowUp = false, want true")
	}

	// Verify it captured the child agent ID
	if dd.followUpAgentID != "child-1" {
		t.Errorf("followUpAgentID = %q, want %q", dd.followUpAgentID, "child-1")
	}

	// Verify it matched the original child's tool label
	if dd.toolLabel != "code" {
		t.Errorf("toolLabel = %q, want %q", dd.toolLabel, "code")
	}

	// Verify the task preview contains the follow-up message
	if !strings.Contains(dd.taskPreview, "add error handling") {
		t.Errorf("taskPreview = %q, want to contain %q", dd.taskPreview, "add error handling")
	}
}

func TestFollowUpHeaderRendersWithChildAgentID(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	b := &contentBuffer{
		styles:        styles,
		segments:      nil,
		collapseState: make(map[int]bool),
		showThinking:  true,
	}

	dd := &delegationDisplayState{
		isFollowUp:      true,
		followUpAgentID: "child-3",
		toolLabel:       "explore",
		status:          "active",
		collapsed:       false,
	}

	header := b.renderDelegationHeader(dd, 80)

	// Header should contain " - follow up child-3"
	if !strings.Contains(header, "follow up") {
		t.Errorf("header doesn't contain 'follow up': %q", header)
	}
	if !strings.Contains(header, "child-3") {
		t.Errorf("header doesn't contain 'child-3': %q", header)
	}
	if !strings.Contains(header, "explore") {
		t.Errorf("header doesn't contain label 'explore': %q", header)
	}
}

func TestFollowUpFallsBackGracefullyWhenChildNotFound(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	// Create a follow_up call for a child that doesn't exist in the buffer
	b.AppendEvent(output.Event{
		Type: output.EventTypeToolCallStarted,
		Payload: output.ToolCallStartedEvent{
			CallID: "parent-1",
			Tool:   "follow_up",
			Arguments: map[string]any{
				"agent_id": "nonexistent-child",
				"message":  "continue work",
			},
		},
	})

	// Should create a delegation segment without a toolLabel
	if len(b.segments) < 1 {
		t.Fatal("no segments created")
	}

	if b.segments[0].kind != segmentDelegation {
		t.Errorf("segments[0].kind = %v, want segmentDelegation", b.segments[0].kind)
	}

	dd := b.segments[0].delegData
	if dd == nil {
		t.Fatal("segments[0].delegData is nil")
	}

	// Should still be marked as follow_up
	if !dd.isFollowUp {
		t.Errorf("isFollowUp = false, want true")
	}

	// Should have the agent ID even though toolLabel is empty
	if dd.followUpAgentID != "nonexistent-child" {
		t.Errorf("followUpAgentID = %q, want %q", dd.followUpAgentID, "nonexistent-child")
	}

	// toolLabel will be empty (fallback)
	// This is acceptable per the spec: "fall back gracefully if the child segment is not found"
}

func useTrueColor(t *testing.T) {
	t.Helper()

	old := lipgloss.Writer.Profile
	lipgloss.Writer.Profile = colorprofile.TrueColor
	t.Cleanup(func() {
		lipgloss.Writer.Profile = old
	})
}
func TestFollowUpCompletionDisplaysPerFollowUpStats(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	b := &contentBuffer{
		styles:            styles,
		segments:          make([]contentSegment, 0),
		collapseState:     make(map[int]bool),
		showThinking:      true,
		activeDelegations: make(map[string]delegationLocator),
	}

	// 1. Parent calls the specialized "code" delegation tool.
	b.AppendEvent(output.Event{
		Type: output.EventTypeToolCallStarted,
		Payload: output.ToolCallStartedEvent{
			CallID:    "parent-code",
			Tool:      "code",
			Arguments: map[string]any{"task": "implement feature"},
		},
	})

	// 2. The original child starts.
	b.AppendEvent(output.Event{
		Type: output.EventTypeDelegationStarted,
		Payload: output.DelegationStartedEvent{
			AgentID:     "child-7",
			TaskPreview: "implement feature",
		},
	})

	// 3. The original child completes with high stats (58 turns, 59 tool calls).
	b.AppendEvent(output.Event{
		Type: output.EventTypeDelegationComplete,
		Payload: output.DelegationCompleteEvent{
			AgentID:       "child-7",
			Status:        "partial",
			TurnCount:     58,
			TokenCount:    9000,
			ToolCallCount: 59,
			Output:        "original child output",
		},
	})

	// Find the original child segment and snapshot its state for later comparison.
	// With merging, it may be in a group or standalone.
	var originalDD *delegationDisplayState
	for _, dd := range delegationStates(b) {
		if dd.agentID == "child-7" {
			originalDD = dd
			break
		}
	}
	if originalDD == nil {
		t.Fatalf("original child segment not found")
	}
	if originalDD.turnCount != 58 || originalDD.toolCallCount != 59 {
		t.Fatalf("original child stats not recorded: turns=%d toolcalls=%d", originalDD.turnCount, originalDD.toolCallCount)
	}

	// 4. Parent issues a follow_up for the same child.
	b.AppendEvent(output.Event{
		Type: output.EventTypeToolCallStarted,
		Payload: output.ToolCallStartedEvent{
			CallID: "parent-followup",
			Tool:   "follow_up",
			Arguments: map[string]any{
				"agent_id": "child-7",
				"message":  "now also add tests",
			},
		},
	})

	// 5. The follow-up's own DelegationStarted event arrives carrying the
	//    same AgentID (the resumed child session reuses child-7).
	b.AppendEvent(output.Event{
		Type: output.EventTypeDelegationStarted,
		Payload: output.DelegationStartedEvent{
			AgentID:     "child-7",
			TaskPreview: "now also add tests",
		},
	})

	// 6. The follow-up completes. In production, the runtime reports
	//    cumulative state.TurnCount from the resumed session, which
	//    includes the original child's turns. A follow-up that uses
	//    zero new turns therefore reports the same TurnCount as the
	//    original child — that is the bug from issue #264.
	b.AppendEvent(output.Event{
		Type: output.EventTypeDelegationComplete,
		Payload: output.DelegationCompleteEvent{
			AgentID:       "child-7",
			Status:        "max_turns",
			TurnCount:     58,
			TokenCount:    9100,
			ToolCallCount: 59,
			Output:        "follow-up output",
		},
	})

	// Locate the follow-up segment: it is the most recent delegation
	// segment marked as a follow-up. With merging, it may be in a group or standalone.
	var followUpDD *delegationDisplayState
	for i := len(b.segments) - 1; i >= 0; i-- {
		seg := b.segments[i]
		if seg.kind == segmentDelegation && seg.delegData != nil && seg.delegData.isFollowUp {
			followUpDD = seg.delegData
			break
		}
		if seg.kind == segmentDelegationGroup && seg.delegGroupData != nil {
			for j := len(seg.delegGroupData.entries) - 1; j >= 0; j-- {
				if seg.delegGroupData.entries[j].isFollowUp {
					followUpDD = seg.delegGroupData.entries[j]
					break
				}
			}
		}
		if followUpDD != nil {
			break
		}
	}
	if followUpDD == nil {
		t.Fatalf("follow-up segment not found")
	}

	// The follow-up must remain marked as a follow-up.
	if !followUpDD.isFollowUp {
		t.Errorf("isFollowUp = false, want true")
	}
	if followUpDD.followUpAgentID != "child-7" {
		t.Errorf("followUpAgentID = %q, want %q", followUpDD.followUpAgentID, "child-7")
	}
	if followUpDD.toolLabel != "code" {
		t.Errorf("toolLabel = %q, want %q (from original child)", followUpDD.toolLabel, "code")
	}

	// The follow-up's prompt must be the follow-up message, not the
	// original child task preview.
	if followUpDD.promptText == "" || followUpDD.promptText == "implement feature" {
		t.Errorf("promptText = %q, want follow-up message", followUpDD.promptText)
	}

	// The follow-up segment must show the follow-up's own stats, not
	// the original child's cumulative stats. A follow-up that used zero
	// new turns should report zero, not the original's 58.
	if followUpDD.turnCount == originalDD.turnCount {
		t.Errorf("follow-up turnCount = %d, matches original child's %d; want follow-up's own delta (0 for a no-op follow-up)",
			followUpDD.turnCount, originalDD.turnCount)
	}
	if followUpDD.toolCallCount == originalDD.toolCallCount {
		t.Errorf("follow-up toolCallCount = %d, matches original child's %d; want follow-up's own delta (0 for a no-op follow-up)",
			followUpDD.toolCallCount, originalDD.toolCallCount)
	}
	if followUpDD.output != "follow-up output" {
		t.Errorf("follow-up output = %q, want %q", followUpDD.output, "follow-up output")
	}

	// The follow-up's status must reflect the follow-up's own completion,
	// not the original child's "partial" status.
	if followUpDD.resultStatus == "partial" {
		t.Errorf("follow-up resultStatus = %q, want %q (follow-up's own status, not original's)",
			followUpDD.resultStatus, "max_turns")
	}

	// The original child segment must not have been mutated by the
	// follow-up's completion event.
	if originalDD.output != "original child output" {
		t.Errorf("original child output = %q, want %q (must not be overwritten by follow-up)",
			originalDD.output, "original child output")
	}

	// Render the follow-up header and confirm it does not display the
	// original child's turn count.
	followUpDD.collapsed = true
	header := b.renderDelegationHeader(followUpDD, 120)
	if strings.Contains(header, "58 turns") {
		t.Errorf("follow-up header shows original child's turn count: %q", header)
	}
	if strings.Contains(header, "59 tool calls") {
		t.Errorf("follow-up header shows original child's tool call count: %q", header)
	}
}

func TestFollowUpCompletionDisplaysCumulativeCacheHitRate(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	b := &contentBuffer{
		styles:            styles,
		segments:          make([]contentSegment, 0),
		collapseState:     make(map[int]bool),
		showThinking:      true,
		activeDelegations: make(map[string]delegationLocator),
	}

	// 1. The original child completes with its own run's cache stats
	// (InputTokens=100, CacheReadTokens=900 -> 90% hit rate).
	b.AppendEvent(output.Event{
		Type: output.EventTypeDelegationStarted,
		Payload: output.DelegationStartedEvent{
			AgentID:     "child-9",
			TaskPreview: "implement feature",
		},
	})
	b.AppendEvent(output.Event{
		Type: output.EventTypeDelegationComplete,
		Payload: output.DelegationCompleteEvent{
			AgentID:           "child-9",
			Status:            "complete",
			TurnCount:         5,
			TokenCount:        1000,
			ToolCallCount:     6,
			InputTokens:       100,
			CacheReadTokens:   900,
			CacheCreateTokens: 0,
			Output:            "original child output",
		},
	})

	// 2. Parent issues a follow_up for the same child.
	b.AppendEvent(output.Event{
		Type: output.EventTypeToolCallStarted,
		Payload: output.ToolCallStartedEvent{
			CallID: "parent-followup",
			Tool:   "follow_up",
			Arguments: map[string]any{
				"agent_id": "child-9",
				"message":  "now also add tests",
			},
		},
	})
	b.AppendEvent(output.Event{
		Type: output.EventTypeDelegationStarted,
		Payload: output.DelegationStartedEvent{
			AgentID:     "child-9",
			TaskPreview: "now also add tests",
		},
	})

	// 3. The follow-up completes. The payload's cache fields are CUMULATIVE
	// across the child's runs (accumulated in internal/delegation), so they
	// carry the child's whole-life totals: the small follow-up run
	// (InputTokens=20, CacheReadTokens=5) is added onto the original
	// (100/900), giving InputTokens=120, CacheReadTokens=905 (~88.3% hit
	// rate). Baseline-subtracting against the original run would be wrong —
	// the payload values are rendered verbatim.
	b.AppendEvent(output.Event{
		Type: output.EventTypeDelegationComplete,
		Payload: output.DelegationCompleteEvent{
			AgentID:           "child-9",
			Status:            "complete",
			TurnCount:         1,
			TokenCount:        25,
			ToolCallCount:     1,
			InputTokens:       120,
			CacheReadTokens:   905,
			CacheCreateTokens: 0,
			Output:            "follow-up output",
		},
	})

	var followUpDD *delegationDisplayState
	for i := len(b.segments) - 1; i >= 0; i-- {
		seg := b.segments[i]
		if seg.kind == segmentDelegation && seg.delegData != nil && seg.delegData.isFollowUp {
			followUpDD = seg.delegData
			break
		}
		if seg.kind == segmentDelegationGroup && seg.delegGroupData != nil {
			for j := len(seg.delegGroupData.entries) - 1; j >= 0; j-- {
				if seg.delegGroupData.entries[j].isFollowUp {
					followUpDD = seg.delegGroupData.entries[j]
					break
				}
			}
		}
		if followUpDD != nil {
			break
		}
	}
	if followUpDD == nil {
		t.Fatalf("follow-up segment not found")
	}

	if !followUpDD.cacheHitOK {
		t.Fatalf("follow-up cacheHitOK = false, want true (cumulative rate must not be dropped)")
	}
	// The payload is cumulative, so the small follow-up run must show the
	// child's OVERALL rate (905/1025, ~88.3%), not the follow-up's own 20%
	// and not a baseline-subtracted figure.
	wantRate := 905.0 / 1025.0
	if diff := followUpDD.cacheHitRate - wantRate; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("follow-up cacheHitRate = %v, want %v (child's cumulative rate, not per-run or baseline-subtracted)", followUpDD.cacheHitRate, wantRate)
	}
	if followUpDD.cacheReadTokens != 905 || followUpDD.inputTokens != 120 {
		t.Errorf("follow-up cache counters = (read=%d, input=%d), want (read=905, input=120) — must equal payload directly, no baseline subtraction", followUpDD.cacheReadTokens, followUpDD.inputTokens)
	}
}

func TestFollowUpScopedEventsRouteToFollowUpSegmentNotOriginal(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	b := &contentBuffer{
		styles:            styles,
		segments:          make([]contentSegment, 0),
		collapseState:     make(map[int]bool),
		showThinking:      true,
		activeDelegations: make(map[string]delegationLocator),
	}

	// Original child delegation lifecycle.
	b.AppendEvent(output.Event{
		Type: output.EventTypeToolCallStarted,
		Payload: output.ToolCallStartedEvent{
			CallID:    "parent-code",
			Tool:      "code",
			Arguments: map[string]any{"task": "implement feature"},
		},
	})
	b.AppendEvent(output.Event{
		Type: output.EventTypeDelegationStarted,
		Payload: output.DelegationStartedEvent{
			AgentID:     "child-8",
			TaskPreview: "implement feature",
		},
	})
	b.AppendEvent(output.Event{
		Type: output.EventTypeDelegationComplete,
		Payload: output.DelegationCompleteEvent{
			AgentID:       "child-8",
			Status:        "complete",
			TurnCount:     5,
			TokenCount:    1000,
			ToolCallCount: 6,
			Output:        "original child output",
		},
	})

	// Follow-up tool call + its DelegationStarted (reusing child-8).
	b.AppendEvent(output.Event{
		Type: output.EventTypeToolCallStarted,
		Payload: output.ToolCallStartedEvent{
			CallID: "parent-followup",
			Tool:   "follow_up",
			Arguments: map[string]any{
				"agent_id": "child-8",
				"message":  "now also add tests",
			},
		},
	})
	b.AppendEvent(output.Event{
		Type: output.EventTypeDelegationStarted,
		Payload: output.DelegationStartedEvent{
			AgentID:     "child-8",
			TaskPreview: "now also add tests",
		},
	})

	// A scoped assistant message from the follow-up's child run must
	// be appended to the follow-up segment, not the original child.
	b.AppendEvent(output.WithAgentScope(
		output.NewAssistantMessageEvent(1, "assistant", "follow-up transcript body"),
		"child-8",
	))

	// Locate the follow-up and original segments. With merging, both may be in a group or standalone.
	var followUpDD *delegationDisplayState
	var originalDD *delegationDisplayState
	for _, dd := range delegationStates(b) {
		if dd.isFollowUp && followUpDD == nil {
			followUpDD = dd
		} else if dd.agentID == "child-8" && originalDD == nil {
			originalDD = dd
		}
	}
	if followUpDD == nil {
		t.Fatalf("follow-up segment not found")
	}
	if originalDD == nil {
		t.Fatalf("original child segment not found")
	}

	// The follow-up segment's transcript must contain the scoped message.
	foundInFollowUp := false
	for _, entry := range followUpDD.entries {
		if entry.kind == delegationTranscriptEntryAssistant &&
			strings.Contains(entry.body, "follow-up transcript body") {
			foundInFollowUp = true
			break
		}
	}
	if !foundInFollowUp {
		t.Errorf("follow-up segment transcript missing scoped assistant message; entries=%v", followUpDD.entries)
	}

	// The original child segment's transcript must not contain it.
	for _, entry := range originalDD.entries {
		if entry.kind == delegationTranscriptEntryAssistant &&
			strings.Contains(entry.body, "follow-up transcript body") {
			t.Errorf("original child segment was polluted with follow-up transcript: %q", entry.body)
		}
	}
}

func TestAdvisorCompleteSetsCacheHitRateFromUsage(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 2, "", nil))
	buffer.AppendEvent(output.NewAdvisorCompleteEvent(output.AdvisorCompleteParams{Model: "advisor-model", UseNumber: 1, MaxUses: 2, Note: "check tests first", CacheReadTokens: 900, InputTokens: 100}))

	seg := buffer.segments[0]
	wantRate, wantOK := usagestats.HitRate(900, 100, 0)
	if !wantOK {
		t.Fatal("test setup: usagestats.HitRate() ok = false, want true")
	}
	if !seg.delegData.cacheHitOK {
		t.Fatal("cacheHitOK = false, want true for a payload with cache token fields")
	}
	if seg.delegData.cacheHitRate != wantRate {
		t.Fatalf("cacheHitRate = %v, want %v", seg.delegData.cacheHitRate, wantRate)
	}

	rendered := stripANSI(buffer.String(80))
	if !strings.Contains(rendered, "cache 90.0%") {
		t.Fatalf("rendered box = %q, want it to contain %q", rendered, "cache 90.0%")
	}
}

func TestAdvisorCompleteRendersTokensAndCacheRowsWhenExpanded(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 2, "", nil))
	buffer.AppendEvent(output.NewAdvisorCompleteEvent(output.AdvisorCompleteParams{
		Model:             "advisor-model",
		UseNumber:         1,
		MaxUses:           2,
		Note:              "check tests first",
		CacheReadTokens:   900,
		CacheCreateTokens: 50,
		InputTokens:       50,
		TokenCount:        200,
	}))

	seg := buffer.segments[0]
	seg.delegData.collapsed = false

	rendered := stripANSI(buffer.String(80))
	if !strings.Contains(rendered, "Tokens:") {
		t.Fatalf("rendered box = %q, want it to contain %q", rendered, "Tokens:")
	}
	if !strings.Contains(rendered, "1.0k in / 200 out") {
		t.Fatalf("rendered box = %q, want it to contain %q", rendered, "1.0k in / 200 out")
	}
	if !strings.Contains(rendered, "Cache:") || !strings.Contains(rendered, "90.0%") {
		t.Fatalf("rendered box = %q, want it to contain Cache: 90.0%%", rendered)
	}
}
