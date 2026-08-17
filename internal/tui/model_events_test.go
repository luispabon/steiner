package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestApplyEventOneshotFinishedClearsState(t *testing.T) {
	t.Parallel()
	ch := make(chan agent.SteerMessage, 4)
	ch <- agent.SteerMessage{Text: "test"}

	m := &Model{
		oneshotRunning: true,
		oneshotPhase:   "plan",
		oneshotSteerCh: ch,
	}
	m.status.oneshotPhase = "plan"
	m.sidebar.oneshotPhase = "plan"

	event := output.NewOneshotFinishedEvent("run-x", nil)
	_ = m.applyEvent(event)

	if m.oneshotRunning {
		t.Error("oneshotRunning = true, want false")
	}
	if m.oneshotPhase != "" {
		t.Errorf("oneshotPhase = %q, want empty", m.oneshotPhase)
	}
	if m.oneshotSteerCh != nil {
		t.Error("oneshotSteerCh = non-nil, want nil")
	}
	if m.status.oneshotPhase != "" {
		t.Errorf("status.oneshotPhase = %q, want empty", m.status.oneshotPhase)
	}
	if m.sidebar.oneshotPhase != "" {
		t.Errorf("sidebar.oneshotPhase = %q, want empty", m.sidebar.oneshotPhase)
	}
}

func TestApplyEventPromotesQueuedStandaloneApprovalPill(t *testing.T) {
	first := &approvalPillData{tool: "bash", mode: "prompt", preview: `{"command":"pwd"}`}
	second := &approvalPillData{tool: "read", mode: "prompt", preview: `{"path":"note.txt"}`}
	m := newModel(Config{}, nil)
	m.content.segments = []contentSegment{
		{kind: segmentApprovalPill, approvalData: first},
		{kind: segmentApprovalPill, approvalData: second},
	}

	first.resolved = true
	if !m.promoteNextApproval() {
		t.Fatal("promoteNextApproval() = false, want true")
	}
	if second.resolved {
		t.Fatal("queued approval = resolved, want unresolved")
	}
	if !m.approval.active || m.approval.tool != second.tool {
		t.Fatalf("active approval = %#v, want queued %q", m.approval, second.tool)
	}
	if m.status.mode != "approval" {
		t.Fatalf("status.mode = %q, want approval", m.status.mode)
	}
}

func TestApplyEventConfigWarningAppendsWithoutTouchingSandbox(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	m := &Model{
		styles: styles,
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        styles,
		},
	}
	m.sidebar.sandboxStatus = "unavailable"
	m.status.sandboxStatus = "unavailable"

	_ = m.applyEvent(output.NewConfigWarningEvent("project_context.max_tokens is deprecated"))

	if m.sidebar.sandboxStatus != "unavailable" {
		t.Errorf("sidebar.sandboxStatus = %q, want %q", m.sidebar.sandboxStatus, "unavailable")
	}
	if m.status.sandboxStatus != "unavailable" {
		t.Errorf("status.sandboxStatus = %q, want %q", m.status.sandboxStatus, "unavailable")
	}
	if len(m.content.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(m.content.segments))
	}
	seg := m.content.segments[0]
	if !strings.Contains(seg.text, "max_tokens is deprecated") {
		t.Errorf("segment text = %q, want config warning text", seg.text)
	}
}

func TestConfigureModelStateSeedsConfigWarnings(t *testing.T) {
	t.Parallel()
	m := newModel(Config{ConfigWarnings: []string{"project_context.max_tokens is deprecated"}}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	var found bool
	for _, seg := range m.content.segments {
		if strings.Contains(seg.text, "max_tokens is deprecated") {
			found = true
		}
	}
	if !found {
		t.Fatalf("content segments = %+v, want a seeded config warning line", m.content.segments)
	}
}

func TestApplyEventModeChangedUpdatesStateAndTranscript(t *testing.T) {
	t.Parallel()
	m := &Model{
		mode: "build",
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
		},
	}

	event := output.NewModeChangedEvent("plan")
	_ = m.applyEvent(event)

	if m.mode != "plan" {
		t.Errorf("mode = %q, want %q", m.mode, "plan")
	}
	if m.sidebar.execMode != "plan" {
		t.Errorf("sidebar.execMode = %q, want %q", m.sidebar.execMode, "plan")
	}
	if m.status.execMode != "plan" {
		t.Errorf("status.execMode = %q, want %q", m.status.execMode, "plan")
	}

	if len(m.content.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(m.content.segments))
	}
	seg := m.content.segments[0]
	if seg.kind != segmentStatus {
		t.Fatalf("segment kind = %v, want segmentStatus", seg.kind)
	}
	if seg.text != "mode → plan" {
		t.Errorf("segment text = %q, want %q", seg.text, "mode → plan")
	}
}

func TestApplyEventContextCompactionFinishedRefreshesBudget(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.sidebar.promptUsed = 9000
	m.sidebar.budgetUsed = 9000
	m.sidebar.contextBudget = 10000

	event := output.Event{
		Type: output.EventTypeContextDiagnostics,
		Payload: output.ContextCompactionEvent{
			Severity:      "done",
			PromptTokens:  1200,
			ContextTokens: 10000,
			TotalTokens:   1200,
			ContextWindow: 10000,
			Status:        "ok",
		},
	}
	_ = m.applyEvent(event)

	if m.sidebar.promptUsed != 1200 {
		t.Errorf("sidebar.promptUsed = %d, want 1200", m.sidebar.promptUsed)
	}
	if m.sidebar.budgetUsed != 1200 {
		t.Errorf("sidebar.budgetUsed = %d, want 1200", m.sidebar.budgetUsed)
	}
	if m.sidebar.contextBudget != 10000 {
		t.Errorf("sidebar.contextBudget = %d, want 10000", m.sidebar.contextBudget)
	}
}

func TestApplyEventMCPStatusRefreshesSnapshotAndWarnsOnce(t *testing.T) {
	t.Parallel()
	m := newModel(Config{MCPEnabled: true}, nil)

	// Async connect: startup snapshot shows every server connecting.
	connecting := output.NewMCPStatusEvent(true, map[string]output.MCPServerState{
		"srv-a": {State: "connecting", Transport: "stdio"},
		"srv-b": {State: "connecting", Transport: "stdio"},
	}, nil)
	_ = m.applyEvent(connecting)
	if m.sidebar.mcpTotal != 2 || m.sidebar.mcpConnected != 0 || m.sidebar.mcpFailed {
		t.Fatalf("sidebar mcp = %d/%d failed=%t, want 0/2 not failed", m.sidebar.mcpConnected, m.sidebar.mcpTotal, m.sidebar.mcpFailed)
	}

	// srv-a connects, srv-b fails: one warning line, sorted snapshot.
	failed := output.NewMCPStatusEvent(true, map[string]output.MCPServerState{
		"srv-a": {State: "connected", Transport: "stdio", Tools: []output.MCPAdvertisedTool{{Name: "echo", Outcome: "registered"}}},
		"srv-b": {State: "failed", Transport: "stdio", Error: "boom"},
	}, nil)
	_ = m.applyEvent(failed)
	if m.sidebar.mcpTotal != 2 || m.sidebar.mcpConnected != 1 || !m.sidebar.mcpFailed {
		t.Fatalf("sidebar mcp = %d/%d failed=%t, want 1/2 failed", m.sidebar.mcpConnected, m.sidebar.mcpTotal, m.sidebar.mcpFailed)
	}
	if len(m.mcpServers) != 2 || m.mcpServers[0].Name != "srv-a" || m.mcpServers[1].Name != "srv-b" {
		t.Fatalf("mcpServers = %+v, want sorted srv-a, srv-b", m.mcpServers)
	}
	if len(m.content.segments) != 1 {
		t.Fatalf("warning segments = %d, want 1", len(m.content.segments))
	}
	if got := m.content.segments[0].text; !strings.Contains(got, `MCP server "srv-b" failed to connect: boom`) {
		t.Fatalf("warning text = %q, want failure line", got)
	}

	// The same failure on the next event must not warn again.
	_ = m.applyEvent(failed)
	if len(m.content.segments) != 1 {
		t.Fatalf("warning segments = %d after repeat event, want still 1", len(m.content.segments))
	}

	// srv-b recovers to connected: the warned flag clears, so a later failure
	// (post-reconnect) warns again.
	recovered := output.NewMCPStatusEvent(true, map[string]output.MCPServerState{
		"srv-a": {State: "connected", Transport: "stdio", Tools: []output.MCPAdvertisedTool{{Name: "echo", Outcome: "registered"}}},
		"srv-b": {State: "connected", Transport: "stdio", Tools: []output.MCPAdvertisedTool{{Name: "echo", Outcome: "registered"}}},
	}, nil)
	_ = m.applyEvent(recovered)
	if m.sidebar.mcpConnected != 2 || m.sidebar.mcpFailed {
		t.Fatalf("sidebar mcp = %d/%d failed=%t, want 2/2 not failed", m.sidebar.mcpConnected, m.sidebar.mcpTotal, m.sidebar.mcpFailed)
	}
	_ = m.applyEvent(failed)
	if len(m.content.segments) != 2 {
		t.Fatalf("warning segments = %d after post-recovery failure, want 2", len(m.content.segments))
	}
}

func TestApplyEventMCPStatusRefreshesOrigins(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)

	event := output.NewMCPStatusEvent(false, nil, map[string]output.MCPToolOrigin{
		"mcp__srv_a__echo": {Server: "srv-a", Tool: "echo"},
	})
	_ = m.applyEvent(event)

	if m.mcpEnabled {
		t.Fatal("mcpEnabled = true, want false")
	}
	origin, ok := m.mcpToolOrigins["mcp__srv_a__echo"]
	if !ok || origin.Server != "srv-a" || origin.Tool != "echo" {
		t.Fatalf("mcpToolOrigins = %+v, want srv-a/echo entry", m.mcpToolOrigins)
	}
	// When origins arrive (post-arm full snapshot), both m.mcpToolOrigins and
	// m.content.mcpToolOrigins are refreshed so transcript attribution catches up.
	contentOrigin, ok := m.content.mcpToolOrigins["mcp__srv_a__echo"]
	if !ok || contentOrigin.Server != "srv-a" || contentOrigin.Tool != "echo" {
		t.Fatalf("content.mcpToolOrigins = %v, want srv-a/echo entry", m.content.mcpToolOrigins)
	}
}

func TestApplyEventMCPStatusPreservesOriginsOnEmptySnapshot(t *testing.T) {
	t.Parallel()
	m := newModel(Config{
		MCPToolOrigins: map[string]MCPToolOrigin{
			"mcp__srv_a__echo": {Server: "srv-a", Tool: "echo"},
		},
	}, nil)
	m.content.mcpToolOrigins = m.mcpToolOrigins

	// Pre-arm states-only snapshot has no origins.
	event := output.NewMCPStatusEvent(true, map[string]output.MCPServerState{
		"srv-a": {State: "connecting"},
	}, nil)
	_ = m.applyEvent(event)

	// Both m.mcpToolOrigins and m.content.mcpToolOrigins should preserve the
	// startup snapshot when the payload carries no origins.
	origin, ok := m.mcpToolOrigins["mcp__srv_a__echo"]
	if !ok || origin.Server != "srv-a" || origin.Tool != "echo" {
		t.Fatalf("mcpToolOrigins = %v, want preserved", m.mcpToolOrigins)
	}
	contentOrigin, ok := m.content.mcpToolOrigins["mcp__srv_a__echo"]
	if !ok || contentOrigin.Server != "srv-a" || contentOrigin.Tool != "echo" {
		t.Fatalf("content.mcpToolOrigins = %v, want preserved", m.content.mcpToolOrigins)
	}
}

func TestRandomAccentResolves(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	msg := setAccentMsg{preset: "random"}
	next, _ := m.handleSetAccentMsg(msg)
	m = next.(*Model)

	if got := m.accentPreset; got != "random" {
		t.Errorf("accentPreset = %q, want random (persisted)", got)
	}

	resolved := resolveAccentPreset("random", func(_ int) int { return 0 })
	if resolved == "" {
		t.Fatal("resolveAccentPreset(random) returned empty, want valid hex")
	}

	isValid := false
	for _, hex := range theme.AccentPresets {
		if resolved == hex {
			isValid = true
			break
		}
	}
	if !isValid {
		t.Errorf("resolved accent %q not found in AccentPresets", resolved)
	}
}

func TestAdvisorFlagResetOnInterruptedRunCompletion(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.content.showThinking = true

	// Advisor starts (interrupt not pending yet).
	_ = m.applyEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 1, "", nil))
	if m.content.activeAdvisorSegment == 0 {
		t.Fatal("activeAdvisorSegment = 0 after AdvisorStarted, want > 0")
	}

	// User interrupts (e.g., presses ESC) — now interruptPending is true.
	m.interruptPending = true

	// AdvisorComplete arrives while interruptPending is true.
	// Before the fix, this would be suppressed.
	// After the fix, it should NOT be suppressed and should reset the flag.
	_ = m.applyEvent(output.NewAdvisorCompleteEvent("advisor-model", 1, 1, "some note", false, nil, 0, 0, 0))

	// AdvisorComplete should have cleared the flag via appendAdvisorEvent → handleAdvisorComplete.
	if m.content.activeAdvisorSegment != 0 {
		t.Fatalf("activeAdvisorSegment = %d after AdvisorComplete, want 0 (should be cleared by handler)", m.content.activeAdvisorSegment)
	}

	// Simulate the run finishing: emit RunFinishedEvent which calls resetTopLevelTerminalState.
	// This is extra protection — even if the flag were somehow stale, resetTopLevelTerminalState would clear it.
	_ = m.applyEvent(output.NewRunFinishedEvent(0, "complete", "done", "", nil))
	if m.content.activeAdvisorSegment != 0 {
		t.Fatalf("activeAdvisorSegment = %d after RunFinishedEvent, want 0 (reset on run finish)", m.content.activeAdvisorSegment)
	}

	// Now emit a primary-model thinking chunk and verify it does NOT land in
	// the advisor box (which should have been closed).
	_ = m.applyEvent(output.NewThinkingChunkEventWithSource(0, "primary thinking", output.ChunkSourceAssistant))

	// The advisor box (if it exists) should have no new entries.
	for i := 0; i < len(m.content.segments); i++ {
		if m.content.segments[i].kind == segmentDelegation && m.content.segments[i].delegData != nil && m.content.segments[i].delegData.isAdvisor {
			if len(m.content.segments[i].delegData.entries) != 0 {
				t.Fatalf("advisor box has entries = %d, want 0 (primary thinking should not route to advisor)", len(m.content.segments[i].delegData.entries))
			}
		}
	}

	// Verify a thinking segment was created for the primary thinking.
	foundThinking := false
	for _, seg := range m.content.segments {
		if seg.kind == segmentThinkingBlock {
			foundThinking = true
			break
		}
	}
	if !foundThinking {
		t.Fatal("thinking segment not found (primary thinking should create normal segment)")
	}
}
