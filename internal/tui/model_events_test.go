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

func TestAdvisorFlagResetOnInterruptedRunCompletion(t *testing.T) {
	m := newModel(Config{}, nil)
	m.content.showThinking = true

	// Advisor starts (interrupt not pending yet).
	_ = m.applyEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 1))
	if m.content.activeAdvisorSegment == 0 {
		t.Fatal("activeAdvisorSegment = 0 after AdvisorStarted, want > 0")
	}

	// User interrupts (e.g., presses ESC) — now interruptPending is true.
	m.interruptPending = true

	// AdvisorComplete arrives while interruptPending is true.
	// Before the fix, this would be suppressed.
	// After the fix, it should NOT be suppressed and should reset the flag.
	_ = m.applyEvent(output.NewAdvisorCompleteEvent("advisor-model", 1, 1, "some note", false, nil, 0, 0))

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
