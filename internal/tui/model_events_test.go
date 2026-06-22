package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestApplyEventOneshotFinishedClearsState(t *testing.T) {
	ch := make(chan string, 4)
	ch <- "test"

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

func TestDelegationExtensionUpdateCounterUnscoped(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	event := output.NewDelegationExtensionEvent("agent-1", 3, 5)
	m = updateModel(t, m, runtimeEventMsg{Event: event})

	if got, want := m.status.extCurrent, 3; got != want {
		t.Errorf("status.extCurrent = %d, want %d", got, want)
	}
	if got, want := m.status.extMax, 5; got != want {
		t.Errorf("status.extMax = %d, want %d", got, want)
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
