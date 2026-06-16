package tui

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
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
	if !seg.delegData.promptCollapsed {
		t.Error("delegData.promptCollapsed = false, want true")
	}
}

func TestAppendEventDelegationComplete(t *testing.T) {
	event := output.NewDelegationCompleteEvent("child-2", "complete", 5, 2000, 0, "")

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
			event: output.NewDelegationCompleteEvent("agent-2", "complete", 1, 100, 0, ""),
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

func TestAppendEventSuppressesTransportOverrideProviderDiagnostic(t *testing.T) {
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
			event:     output.NewDelegationCompleteEvent("test-agent", "complete", 2, 500, 0, ""),
			wantMatch: "delegate: complete test-agent (2 turns)",
		},
		{
			name:      "complete_has_tool_calls",
			event:     output.NewDelegationCompleteEvent("test-agent", "complete", 2, 500, 3, ""),
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
	buffer.AppendEvent(output.NewDelegationCompleteEvent("life-agent", "done", 3, 600, 0, ""))
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

	buffer.AppendEvent(output.NewDelegationCompleteEvent("toggle-agent", "done", 1, 50, 0, "result text"))

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
	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	task := strings.Repeat("inspect docs carefully ", 6)
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "inspect docs"))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "delegate", "call_delegate_1", map[string]any{
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
	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	task := strings.Repeat("inspect docs carefully ", 6)
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "delegate", "call_delegate_1", map[string]any{"task": task}))
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
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
	}
	longOutput := strings.Repeat("x", 650) + "tail marker"

	buffer.AppendEvent(output.NewDelegationCompleteEvent("long-agent", "done", 1, 50, 0, longOutput))
	buffer.ToggleLastDelegationOutput()

	rendered := buffer.String(80)
	if !strings.Contains(rendered, "tail") || !strings.Contains(rendered, "marker") {
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
				b.AppendEvent(output.NewDelegationCompleteEvent("c-agent", "done", 7, 300, 3, ""))
			},
			checks: []string{"c-agent", "7 turns", "3 tool calls"},
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

func TestRenderDelegationCollapsedActiveShowsSpinnerAndLatestOperation(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "initial task preview"))
	buffer.AppendEvent(output.WithAgentScope(
		output.NewToolCallStartedEvent(1, "read", "call_1", map[string]any{"path": "README.md"}),
		"child-1",
	))

	rendered := buffer.String(80)
	for _, want := range []string{"delegate", "child-1", "⠋", "read: README.md:1–200", "ctrl+x or click header to expand"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("collapsed active delegation render %q missing %q", rendered, want)
		}
	}
}

func TestRenderDelegationExpandedShowsAssistantAndLightweightToolRows(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "delegate", "call_delegate_1", map[string]any{
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
	buffer.AppendEvent(output.NewDelegationCompleteEvent("child-1", "complete", 2, 25, 0, "final child output"))
	buffer.ToggleLastDelegationOutput()

	rendered := buffer.String(80)
	for _, want := range []string{"delegate", "child-1", "prompt", "child assistant reply", "bash", "pwd", "✓", "output", "final child output", "ctrl+x or click header to collapse"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expanded delegation render %q missing %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "┌") && strings.Contains(rendered, "bash") && strings.Contains(rendered, "pwd") && strings.Count(rendered, "┌") > 1 {
		t.Fatalf("expanded delegation child tool should not render like a nested boxed tool call: %q", rendered)
	}
}

func TestRenderDelegationPromptSubsectionCollapsedAndExpanded(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
	}

	prompt := "inspect the prompt layout\nwith a line that wraps nicely"
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "delegate", "call_delegate_1", map[string]any{
		"task": prompt,
	}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", prompt))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", "child assistant reply"), "child-1"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent("child-1", "complete", 1, 10, 0, "final child output"))

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
		if assistantLine == -1 && strings.Contains(line, "child assistant reply") {
			assistantLine = i
		}
	}
	if promptLine == -1 || assistantLine == -1 || assistantLine <= promptLine {
		t.Fatalf("expanded prompt render %q ordered incorrectly", renderedExpanded)
	}
	if idxPrompt := strings.Index(renderedExpanded, "prompt"); idxPrompt == -1 || strings.Index(renderedExpanded, "child assistant reply") < idxPrompt {
		t.Fatalf("expanded prompt render %q ordered incorrectly", renderedExpanded)
	}
}

func TestRenderDelegationBlankPromptSkipsSubsection(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", ""))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "delegate", "call_delegate_1", map[string]any{
		"task": "",
	}))
	buffer.AppendEvent(output.NewDelegationCompleteEvent("child-1", "complete", 1, 10, 0, "final child output"))
	buffer.ToggleLastDelegationOutput()

	rendered := buffer.String(80)
	if strings.Contains(rendered, "prompt") {
		t.Fatalf("blank prompt render %q should skip subsection", rendered)
	}
}

func TestRenderNormalParentBashToolRemainsBoxed(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
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
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "delegate", "call_delegate_1", map[string]any{
		"task": "inspect docs",
	}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "inspect docs"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent("child-1", "complete", 1, 10, 0, ""))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	if got := buffer.segments[0].kind; got != segmentDelegation {
		t.Fatalf("segment kind = %v, want segmentDelegation", got)
	}

	rendered := buffer.String(80)
	for _, want := range []string{"delegate", "child-1", "✓", "complete"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("delegation lifecycle render %q missing %q", rendered, want)
		}
	}
}

func TestRenderDelegationTranscriptTruncatesToRecentRows(t *testing.T) {
	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
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

	// session_health events no longer produce content buffer segments; health state
	// is stored in model fields and rendered by renderContextInfoLine in the layout.
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

func TestAppendEventStreamsThinkingAndAssistantChunks(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewThinkingChunkEvent(1, "internal reasoning"))
	buffer.AppendEvent(output.NewAssistantChunkEvent(1, `{"intent":"inspect","next":"read file"}`))
	buffer.AppendEvent(output.NewAssistantChunkEvent(1, "visible answer"))
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
	// New collapsed format has "▸ Thinking" on its own line and "▎ first line" on a separate line
	plain := stripANSI(renderedCollapsed)
	if !strings.Contains(plain, "▸ Thinking") || !strings.Contains(plain, "first line") {
		t.Fatalf("collapsed render = %q, want collapsed with '▸ Thinking' and 'first line' separately", plain)
	}
}

func TestThinkingBlockBeforeToolCallStartsToolBoxOnFreshLine(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
		showThinking:  true,
	}

	buffer.AppendEvent(output.NewThinkingChunkEvent(1, "inspect renderer"))
	buffer.finalizeThinkingBlock()
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "read", "call_1", map[string]any{"path": "internal/tui/content_tool.go"}))

	rendered := stripANSI(buffer.String(80))
	lines := strings.Split(rendered, "\n")

	thinkingLine := -1
	for i, line := range lines {
		if strings.Contains(line, "Thinking") {
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
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
		showThinking:  true,
	}

	buffer.AppendEvent(output.NewThinkingChunkEvent(1, "internal reasoning"))
	buffer.AppendEvent(output.NewAssistantChunkEvent(1, `{"intent":"inspect","next":"read file"}`))
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

func TestRenderThinkingBlockSegment(t *testing.T) {
	tests := []struct {
		name      string
		width     int
		thinkData *thinkingBlockData
		want      []string // strings that must appear in result
		notWant   []string // strings that must NOT appear
	}{
		{
			name:      "nil thinkData",
			width:     80,
			thinkData: nil,
			want:      []string{""},
			notWant:   []string{"Thinking"},
		},
		{
			name:  "expanded state, short text",
			width: 80,
			thinkData: &thinkingBlockData{
				body:      "short thought",
				collapsed: false,
				streaming: false,
			},
			want:    []string{"▾ Thinking", "short thought", "▎"},
			notWant: []string{"▸", "…"},
		},
		{
			name:  "expanded state, long line wraps",
			width: 40,
			thinkData: &thinkingBlockData{
				body:      strings.Repeat("a", 80),
				collapsed: false,
				streaming: false,
			},
			want:    []string{"▾ Thinking"},
			notWant: []string{"▸"},
		},
		{
			name:  "collapsed state, 2 lines (≤3)",
			width: 80,
			thinkData: &thinkingBlockData{
				body:      "first line\nsecond line",
				collapsed: true,
				streaming: false,
			},
			want:    []string{"▸ Thinking", "first line", "second line"},
			notWant: []string{"…", "▾"},
		},
		{
			name:  "collapsed state, 5 lines (>3)",
			width: 80,
			thinkData: &thinkingBlockData{
				body:      "line1\nline2\nline3\nline4\nline5",
				collapsed: true,
				streaming: false,
			},
			want:    []string{"▸ Thinking", "line1", "line2", "line3", "…"},
			notWant: []string{"line4", "line5", "▾"},
		},
		{
			name:  "collapsed state at wide width",
			width: 120,
			thinkData: &thinkingBlockData{
				body:      "first\nsecond\nthird",
				collapsed: true,
				streaming: false,
			},
			want:    []string{"▸ Thinking", "first", "second", "third"},
			notWant: []string{"…", "▾"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf := &contentBuffer{styles: theme.BuildStyles(theme.AccentAmber)}
			seg := contentSegment{kind: segmentThinkingBlock, thinkData: tc.thinkData}
			result := buf.renderThinkingBlockSegment(seg, tc.width)
			plain := stripANSI(result)

			// Check want strings
			for _, w := range tc.want {
				if w == "" {
					// Special case: empty string check means result should be empty
					if plain != "" {
						t.Errorf("result should be empty, got: %q", plain)
					}
					continue
				}
				if !strings.Contains(plain, w) {
					t.Errorf("result missing %q\ngot: %q", w, plain)
				}
			}

			// Check notWant strings
			for _, n := range tc.notWant {
				if strings.Contains(plain, n) {
					t.Errorf("result should not contain %q\ngot: %q", n, plain)
				}
			}

			// For the "long line wraps" case, verify no line exceeds width
			if tc.name == "expanded state, long line wraps" {
				for _, line := range strings.Split(plain, "\n") {
					// Remove the "▎ " or "▾ " or "▸ " prefix for width check
					content := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(line, "▎ "), "▾ "), "▸ ")
					if len([]rune(content)) > tc.width {
						t.Errorf("line exceeds width %d: %q (content: %q)", tc.width, line, content)
					}
				}
			}
		})
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

func TestUserSegmentMargin(t *testing.T) {
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
				{kind: segmentAssistantProse, text: "hello from assistant one"},
				{kind: segmentUser, text: "user message"},
				{kind: segmentAssistantProse, text: "hello from assistant two"},
			},
			wantMarginAbove: true,
			wantMarginBelow: true,
		},
		{
			name: "user followed by assistant",
			segments: []contentSegment{
				{kind: segmentUserMarkdown, text: "# User Title\ncontent"},
				{kind: segmentAssistantProse, text: "assistant response"},
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
			wantMarginBetween: true,
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
				{kind: segmentAssistantProse, text: "assistant says something"},
				{kind: segmentUser, text: "user reply"},
			},
			wantMarginAbove: true,
			wantMarginBelow: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &contentBuffer{
				styles:        theme.BuildStyles(theme.AccentAmber),
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
				return !strings.Contains(line, "\u2503") && lipgloss.Width(line) == 80
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
			if tt.wantMarginBetween {
				foundGap := false
				for i := 1; i < len(userLineIdxs); i++ {
					if userLineIdxs[i] > userLineIdxs[i-1]+1 {
						foundGap = true
						break
					}
				}
				if !foundGap {
					t.Errorf("want blank line between consecutive user segments, but all user lines are contiguous")
				}
			}
		})
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
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "delegate", "call_delegate_1", map[string]any{
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
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "fix the bug in module X"))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "delegate", "call_delegate_1", map[string]any{
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
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "do work"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEvent(1, "hello "), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEvent(1, "world"), "child-1"))

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
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		showThinking:  true,
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "do work"))
	buffer.AppendEvent(output.WithAgentScope(output.NewThinkingChunkEvent(1, "plan "), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewThinkingChunkEvent(1, "search"), "child-1"))

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
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
		showThinking:  true,
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "delegate", "call_delegate_1", map[string]any{
		"task": "inspect docs",
	}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "inspect docs"))
	buffer.AppendEvent(output.WithAgentScope(output.NewThinkingChunkEvent(1, "inspect files"), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", "child assistant reply"), "child-1"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent("child-1", "complete", 2, 25, 0, "final child output"))
	buffer.ToggleLastDelegationOutput()

	rendered := stripANSI(buffer.String(80))
	for _, want := range []string{"delegate", "child-1", "Thinking", "inspect files", "child assistant reply"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expanded delegation render %q missing %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "▾ Thinking") || strings.Contains(rendered, "▸ Thinking") {
		t.Fatalf("delegation child thinking should render inline, not as a top-level thinking block: %q", rendered)
	}
}

func TestAppendEventScopedChildAssistantDuplicateFinalMessageSuppressed(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewDelegationStartedEvent("child-1", "do work"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEvent(1, "hello"), "child-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEvent(1, " world"), "child-1"))
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
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantChunkEvent(1, "second child answer"), "child-2"))

	first := buffer.segments[0].delegData
	second := buffer.segments[1].delegData
	if first == nil || second == nil {
		t.Fatal("delegData = nil, want delegation state on both segments")
	}
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
	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
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
	buffer := &contentBuffer{
		styles: theme.BuildStyles(theme.AccentAmber),
	}

	tests := []struct {
		name string
		tool string
		want lipgloss.Color
	}{
		{name: "bash", tool: "bash", want: lipgloss.Color(theme.ToolAmberLine)},
		{name: "read", tool: "read", want: lipgloss.Color(theme.ToolCyanLine)},
		{name: "mutate", tool: "mutate", want: lipgloss.Color(theme.ToolGrnLine)},
		{name: "grep", tool: "grep", want: lipgloss.Color(theme.ToolMagLine)},
		{name: "glob", tool: "glob", want: lipgloss.Color(theme.ToolBlueLine)},
		{name: "todo", tool: "todo", want: lipgloss.Color(theme.WarnLine)},
		{name: "default", tool: "ls", want: lipgloss.Color(theme.ToolBlueLine)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buffer.toolBorderStyle(tt.tool).GetForeground()
			if got != tt.want {
				t.Fatalf("toolBorderStyle(%q) foreground = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

func TestRenderAdjacentSameToolCallsAsOneGroupedBox(t *testing.T) {
	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
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
	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
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

func TestRenderMixedAdjacentToolsStaySeparate(t *testing.T) {
	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "pwd"}))
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "read", "call_2", map[string]any{"path": "README.md"}))

	if got := len(buffer.segments); got != 2 {
		t.Fatalf("segments = %d, want 2 separate segments", got)
	}
	rendered := stripANSI(buffer.String(140))
	if got := strings.Count(rendered, "┌"); got != 2 {
		t.Fatalf("mixed top borders = %d, want 2", got)
	}
}

func TestRenderNonToolSegmentBreaksToolGrouping(t *testing.T) {
	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
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
	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
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
	if got := group.entries[0].meta; got != "✅" {
		t.Fatalf("first grouped call meta = %q, want ✅", got)
	}
	if group.entries[0].hasError {
		t.Fatal("first grouped call hasError = true, want false")
	}
	if got := group.entries[1].body; got != "bad" {
		t.Fatalf("second grouped call body = %q, want bad", got)
	}
	if got := group.entries[1].meta; got != "❌" {
		t.Fatalf("second grouped call meta = %q, want ❌", got)
	}
	if !group.entries[1].hasError {
		t.Fatal("second grouped call hasError = false, want true")
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

func TestIsSpecializedDelegateTool(t *testing.T) {
	tests := []struct {
		tool string
		want bool
	}{
		{"explore", true},
		{"research", true},
		{"code", true},
		{"plan", true},
		{"verify", true},
		// case-insensitive
		{"Explore", true},
		{"RESEARCH", true},
		{"Code", true},
		// whitespace trimmed
		{" plan ", true},
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
			tool: "plan",
			args: map[string]any{"task": "plan the migration"},
			want: "plan the migration",
		},
		{
			tool: "verify",
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
	tools := []string{"explore", "research", "code", "plan", "verify"}
	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
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
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		styles:        theme.BuildStyles(theme.AccentAmber),
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

func TestDelegationHeaderKeepsDelegateLabelForBaseDelegate(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}
	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "delegate", "call-2", map[string]any{"task": "do work"}))

	if len(buffer.segments) == 0 || buffer.segments[0].delegData == nil {
		t.Fatal("expected delegation segment")
	}
	dd := buffer.segments[0].delegData
	if dd.toolLabel != "" {
		t.Errorf("toolLabel = %q, want empty for base delegate tool", dd.toolLabel)
	}
	header := buffer.renderDelegationHeader(dd, 80)
	if !strings.Contains(header, "delegate") {
		t.Errorf("delegation header %q missing %q for base delegate", header, "delegate")
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
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "delegate", "call_1", map[string]any{"task": "do work"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("agent-1", "do work"))
	buffer.AppendEvent(output.WithAgentScope(output.NewModelCallStartedEvent(1, "qwen3-coder-30b", 12), "agent-1"))
	buffer.AppendEvent(output.WithAgentScope(
		output.NewContextTokenBudgetEvent("", 1, 2400, 200000, 1.2, 90.0, 0, 0, "", false),
		"agent-1",
	))
	buffer.AppendEvent(output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", "working on it"), "agent-1"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent("agent-1", "complete", 5, 1234, 3, "result"))
	buffer.ToggleLastDelegationOutput()

	rendered := stripANSI(buffer.String(100))
	for _, want := range []string{"model qwen3-coder-", "30b", "Turns: 5", "Tool Calls: 3", "Tokens: 1234", "Duration:", "Status: complete", "Ctx: 1%"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("expanded delegation render missing %q:\n%s", want, rendered)
		}
	}
	if !strings.Contains(rendered, "2.4k / 200k") {
		t.Errorf("expanded delegation render missing context capacity:\n%s", rendered)
	}
	if !strings.Contains(rendered, "────────────────") {
		t.Errorf("expanded delegation render missing footer separator:\n%s", rendered)
	}
}

func TestDelegationStatsFooterHiddenWhenCollapsed(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "delegate", "call_1", map[string]any{"task": "do work"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("agent-1", "do work"))
	buffer.AppendEvent(output.NewDelegationCompleteEvent("agent-1", "complete", 5, 1234, 0, "result"))

	// collapsed (default) — stats footer must not appear
	rendered := stripANSI(buffer.String(100))
	for _, unexpected := range []string{"Turns: 5", "Tokens: 1234", "Status: complete"} {
		if strings.Contains(rendered, unexpected) {
			t.Errorf("collapsed delegation render should not contain %q:\n%s", unexpected, rendered)
		}
	}
}

func TestDelegationStatsFooterVisibleWhenExpandedActive(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        theme.BuildStyles(theme.AccentAmber),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "plan", "call_1", map[string]any{"task": "plan work"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("agent-1", "plan work"))
	buffer.AppendEvent(output.WithAgentScope(
		output.NewAPIRequestEvent("deepseek-v4-flash", nil, nil, nil, nil, prompt.ModelTokenBudget{}),
		"agent-1",
	))
	buffer.AppendEvent(output.WithAgentScope(
		output.NewContextTokenBudgetEvent("", 1, 80000, 200000, 42.5, 90.0, 0, 0, "", false),
		"agent-1",
	))
	buffer.ToggleLastDelegationOutput()

	rendered := stripANSI(buffer.String(100))
	for _, want := range []string{"model deepseek-v4-flash", "Duration:", "Status: active", "Ctx: 43% (80k / 200k)"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("expanded active delegation render missing %q:\n%s", want, rendered)
		}
	}
}

func TestBuildMutateLinesRendersOperations(t *testing.T) {
	buffer := &contentBuffer{
		styles: theme.BuildStyles(theme.AccentAmber),
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
				{Type: "line_replace", Path: "line.go", Line: 5, OldString: "foo", NewString: "bar"},
				{Type: "delete", Path: "old.go"},
				{Type: "move", From: "a.go", To: "b.go"},
			},
		},
	}

	lines := buffer.buildMutateLines(tc, 80)
	got := stripANSI(strings.Join(lines, "\n"))

	// Summary
	if !strings.Contains(got, "5 operations") {
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
	if !strings.Contains(got, "line.go") {
		t.Fatalf("missing line_replace path: %q", got)
	}
	if !strings.Contains(got, "old.go") {
		t.Fatalf("missing delete path: %q", got)
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

	// line_replace should show line number and diff
	if !strings.Contains(got, "- foo") {
		t.Fatalf("missing line_replace old: %q", got)
	}
	if !strings.Contains(got, "+ bar") {
		t.Fatalf("missing line_replace new: %q", got)
	}

	// delete should NOT have content block (no rule lines after header)
	// move should NOT have content block
}

func TestBuildMutateLinesFallsBackToPlainWhenEmptyOperations(t *testing.T) {
	buffer := &contentBuffer{
		styles: theme.BuildStyles(theme.AccentAmber),
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
	buffer := &contentBuffer{
		styles: theme.BuildStyles(theme.AccentAmber),
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
				{tool: "mutate", args: "replace foo.go", meta: "✅", collapsed: true},
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
				{tool: "mutate", meta: "✅"},
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
			checks: map[int]int{0: 2}, // header + 1 preview line
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
			checks: map[int]int{0: 2}, // header + 1 body line
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
			checks: map[int]int{0: 2, 1: 3}, // thinking=2, tool=3; blank line between not attributed
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
	styles := theme.BuildStyles(theme.AccentAmber)

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
func useTrueColor(t *testing.T) {
	t.Helper()

	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(old)
	})
}
