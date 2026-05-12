package agent

import (
	"testing"
)

func TestEpochManagerMaskingWindowDefault(t *testing.T) {
	t.Parallel()
	e := &EpochManager{}
	if got, want := e.MaskingWindow(), defaultMaskingWindowTurns; got != want {
		t.Fatalf("MaskingWindow() = %d, want %d", got, want)
	}
}

func TestEpochManagerMaskingWindowConfigured(t *testing.T) {
	t.Parallel()
	e := &EpochManager{maskingWindowTurns: 10}
	if got, want := e.MaskingWindow(), 10; got != want {
		t.Fatalf("MaskingWindow() = %d, want %d", got, want)
	}
}

func TestEpochManagerInitializeFromTurnCount(t *testing.T) {
	t.Parallel()
	e := &EpochManager{maskingWindowTurns: 5}
	e.InitializeFromTurnCount(12)
	if got, want := e.epochStartTurn, 12; got != want {
		t.Fatalf("epochStartTurn = %d, want %d", got, want)
	}
	if got, want := e.epochMaskBoundary, 7; got != want {
		t.Fatalf("epochMaskBoundary = %d, want %d", got, want)
	}
}

func TestEpochManagerInitializeFromTurnCountIdempotent(t *testing.T) {
	t.Parallel()
	e := &EpochManager{maskingWindowTurns: 5}
	e.InitializeFromTurnCount(12)
	// Second call must be a no-op.
	e.InitializeFromTurnCount(20)
	if got, want := e.epochStartTurn, 12; got != want {
		t.Fatalf("epochStartTurn after second call = %d, want %d (no-op)", got, want)
	}
	if got, want := e.epochMaskBoundary, 7; got != want {
		t.Fatalf("epochMaskBoundary after second call = %d, want %d (no-op)", got, want)
	}
}

func TestEpochManagerShouldAdvance(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		window      int
		startTurn   int
		currentTurn int
		want        bool
	}{
		{
			name:        "advances when delta equals window",
			window:      5,
			startTurn:   10,
			currentTurn: 15,
			want:        true,
		},
		{
			name:        "advances when delta exceeds window",
			window:      5,
			startTurn:   10,
			currentTurn: 20,
			want:        true,
		},
		{
			name:        "no advance when delta less than window",
			window:      5,
			startTurn:   10,
			currentTurn: 14,
			want:        false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &EpochManager{
				maskingWindowTurns: tt.window,
				epochStartTurn:     tt.startTurn,
			}
			got := e.ShouldAdvance(tt.currentTurn, RunState{})
			if got != tt.want {
				t.Fatalf("ShouldAdvance(%d) = %v, want %v", tt.currentTurn, got, tt.want)
			}
		})
	}
}

func TestEpochManagerAdvance(t *testing.T) {
	t.Parallel()
	e := &EpochManager{maskingWindowTurns: 5, epochStartTurn: 10}
	trigger := e.Advance(15)
	if got, want := trigger, "turn_count"; got != want {
		t.Fatalf("trigger = %q, want %q", got, want)
	}
	if got, want := e.epochStartTurn, 15; got != want {
		t.Fatalf("epochStartTurn = %d, want %d", got, want)
	}
	if got, want := e.epochMaskBoundary, 10; got != want {
		t.Fatalf("epochMaskBoundary = %d, want %d (currentTurn - window)", got, want)
	}
}

func TestEpochManagerReset(t *testing.T) {
	t.Parallel()
	e := &EpochManager{
		maskingWindowTurns: 5,
		epochMaskBoundary:  7,
		epochStartTurn:     12,
	}
	e.Reset(13, "compaction")
	if got, want := e.epochMaskBoundary, 0; got != want {
		t.Fatalf("epochMaskBoundary = %d, want %d", got, want)
	}
	if got, want := e.epochStartTurn, 13; got != want {
		t.Fatalf("epochStartTurn = %d, want %d", got, want)
	}
}

func TestEpochManagerUpdateMinVisibleTurn(t *testing.T) {
	t.Parallel()
	e := &EpochManager{}
	messages := []Message{
		{Role: MessageRoleUser, Content: "u1", Turn: 3},
		{Role: MessageRoleAssistant, Content: "a1", Turn: 3},
		{Role: MessageRoleUser, Content: "u2", Turn: 7},
		{Role: MessageRoleAssistant, Content: "a2", Turn: 7},
	}
	e.UpdateMinVisibleTurn(messages)
	if got, want := e.minVisibleTurn, 3; got != want {
		t.Fatalf("minVisibleTurn = %d, want %d", got, want)
	}
}

func TestEpochManagerMinVisibleTurn(t *testing.T) {
	t.Parallel()
	e := &EpochManager{minVisibleTurn: 4}
	if got, want := e.MinVisibleTurn(), 4; got != want {
		t.Fatalf("MinVisibleTurn() = %d, want %d", got, want)
	}
}

func TestEpochManagerMaskBoundary(t *testing.T) {
	t.Parallel()
	e := &EpochManager{epochMaskBoundary: 9}
	if got, want := e.MaskBoundary(), 9; got != want {
		t.Fatalf("MaskBoundary() = %d, want %d", got, want)
	}
}
