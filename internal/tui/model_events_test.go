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
