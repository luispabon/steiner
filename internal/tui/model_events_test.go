package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestApplyEventOneshotFinishedClearsState(t *testing.T) {
	ch := make(chan agent.SteerMessage, 4)
	ch <- agent.SteerMessage{Text: "test"}

	m := Model{
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

func TestApplyEventModeChangedUpdatesStateAndTranscript(t *testing.T) {
	m := Model{
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

func TestRandomAccentResolves(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	msg := setAccentMsg{preset: "random"}
	next, _ := m.handleSetAccentMsg(msg)
	m = next.(Model)

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

func TestAdvisorFlagResetOnRunFinish(t *testing.T) {
	// Regression test: advisor flag should not persist across runs.
	// If an advisor call is interrupted, the activeAdvisorSegment flag
	// must be cleared so that primary-model thinking chunks don't land
	// in the stale advisor box.
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		showThinking:  true,
	}

	// Start advisor call.
	buffer.AppendEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 1))
	if buffer.activeAdvisorSegment == 0 {
		t.Fatal("activeAdvisorSegment = 0 after AdvisorStarted, want > 0")
	}

	// Simulate interrupt by manually setting the flag (in real usage, shouldSuppressInterruptedRunEvent
	// would prevent AdvisorComplete from being processed, leaving the flag stale).
	// For this test, we simulate that by setting the flag back after completion.
	staleFlag := buffer.activeAdvisorSegment

	// Emit AdvisorComplete.
	buffer.AppendEvent(output.NewAdvisorCompleteEvent("advisor-model", 1, 1, "some note", false, nil, 0, 0))
	if buffer.activeAdvisorSegment != 0 {
		t.Fatalf("activeAdvisorSegment = %d after AdvisorComplete, want 0", buffer.activeAdvisorSegment)
	}

	// Manually set the flag to simulate the stuck-flag bug (as if AdvisorComplete was suppressed).
	buffer.activeAdvisorSegment = staleFlag

	// Now reset the advisor segment as would happen on run finish.
	buffer.ResetAdvisorSegment()
	if buffer.activeAdvisorSegment != 0 {
		t.Fatalf("activeAdvisorSegment = %d after ResetAdvisorSegment, want 0", buffer.activeAdvisorSegment)
	}

	// Record segment count before the thinking chunk.
	segmentsBefore := len(buffer.segments)

	// Verify that a primary-model thinking chunk now goes to a normal segment.
	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(0, "primary thinking", output.ChunkSourceAssistant))

	// A new thinking segment should be created (not appended to advisor box).
	// The advisor box's delegData.entries should remain empty.
	advisorBox := buffer.segments[0]
	if advisorBox.delegData != nil && len(advisorBox.delegData.entries) != 0 {
		t.Fatalf("advisor box entries = %d, want 0 (thinking chunk should not land in advisor)", len(advisorBox.delegData.entries))
	}

	// Find the thinking segment we just created.
	foundThinking := false
	for i := segmentsBefore; i < len(buffer.segments); i++ {
		if buffer.segments[i].kind == segmentThinkingBlock {
			foundThinking = true
			break
		}
	}
	if !foundThinking {
		t.Fatal("thinking segment not found after emitting primary thinking chunk")
	}
}
