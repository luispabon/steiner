package agent

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
)

// captureEvents collects emitted events for assertions.
type captureEvents struct {
	events []output.Event
}

func (c *captureEvents) Emit(e output.Event) {
	c.events = append(c.events, e)
}

func TestScratchpadManagerOnTurnCompleteHybrid(t *testing.T) {
	sink := &captureEvents{}
	m := &ScratchpadManager{mode: config.ScratchpadModeHybrid}
	m.SetEventSink(sink)

	// Two misses — no event yet
	m.OnTurnComplete(1, false)
	m.OnTurnComplete(2, false)
	if len(sink.events) != 0 {
		t.Fatalf("want 0 events after 2 misses, got %d", len(sink.events))
	}

	// Third miss — event emitted
	m.OnTurnComplete(3, false)
	if len(sink.events) == 0 {
		t.Fatal("want event after 3 consecutive misses, got none")
	}
}

func TestScratchpadManagerOnTurnCompleteResetOnCall(t *testing.T) {
	sink := &captureEvents{}
	m := &ScratchpadManager{mode: config.ScratchpadModeHybrid}
	m.SetEventSink(sink)

	m.OnTurnComplete(1, false)
	m.OnTurnComplete(2, false)
	// Reset by calling with scratchpadCalled=true
	m.OnTurnComplete(3, true)
	// Now two more misses — still below threshold
	m.OnTurnComplete(4, false)
	m.OnTurnComplete(5, false)
	if len(sink.events) != 0 {
		t.Fatalf("want 0 events — failures reset by call, got %d", len(sink.events))
	}
}

func TestScratchpadManagerOnTurnCompleteNonHybridNoOp(t *testing.T) {
	sink := &captureEvents{}
	m := &ScratchpadManager{mode: config.ScratchpadModeScaffoldOnly}
	m.SetEventSink(sink)

	for i := 0; i < 5; i++ {
		m.OnTurnComplete(i, false)
	}
	if len(sink.events) != 0 {
		t.Fatalf("want no events in scaffold-only mode, got %d", len(sink.events))
	}
}

func TestScratchpadManagerIngestToolResult(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantIntent string
		wantNext   string
		wantAck    string
	}{
		{
			name:       "valid payload updates scratchpad",
			content:    `{"intent":"fix bug","decisions":"read the code","open":"","next":"run tests"}`,
			wantIntent: "fix bug",
			wantNext:   "run tests",
			wantAck:    `{"ok":true}`,
		},
		{
			name:    "missing field returns ack without panic",
			content: `{"intent":"x","decisions":"d","open":"o"}`,
			wantAck: `{"ok":true}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &ScratchpadManager{mode: config.ScratchpadModeHybrid}
			got := m.IngestToolResult(1, tc.content)
			if got != tc.wantAck {
				t.Errorf("IngestToolResult() = %q, want %q", got, tc.wantAck)
			}
			if tc.wantIntent != "" && m.scratchpad.Intent != tc.wantIntent {
				t.Errorf("Intent = %q, want %q", m.scratchpad.Intent, tc.wantIntent)
			}
			if tc.wantNext != "" && m.scratchpad.Next != tc.wantNext {
				t.Errorf("Next = %q, want %q", m.scratchpad.Next, tc.wantNext)
			}
		})
	}
}

func TestScratchpadManagerIngestToolResultInvalidJSON(t *testing.T) {
	m := &ScratchpadManager{}
	got := m.IngestToolResult(1, "not-json{{{")
	if got != `{"ok":true}` {
		t.Errorf("IngestToolResult() with invalid JSON = %q, want ack", got)
	}
	// State should be unchanged
	if m.scratchpad.Intent != "" {
		t.Errorf("Intent should remain empty on invalid JSON, got %q", m.scratchpad.Intent)
	}
}

func TestScratchpadManagerAppendDecisionFact(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		fact     string
		want     string
	}{
		{
			name:     "empty fact ignored",
			existing: "existing",
			fact:     "",
			want:     "existing",
		},
		{
			name:     "none fact ignored",
			existing: "existing",
			fact:     "none",
			want:     "existing",
		},
		{
			name:     "fact appended",
			existing: "first",
			fact:     "second",
			want:     "first\nsecond",
		},
		{
			name:     "first fact on empty",
			existing: "",
			fact:     "first",
			want:     "first",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &ScratchpadManager{}
			m.scratchpad.Decisions = tc.existing
			m.AppendDecisionFact(tc.fact)
			if m.scratchpad.Decisions != tc.want {
				t.Errorf("Decisions = %q, want %q", m.scratchpad.Decisions, tc.want)
			}
		})
	}

	t.Run("bounded by decisionsMaxLines", func(t *testing.T) {
		m := &ScratchpadManager{}
		for i := 0; i < decisionsMaxLines+5; i++ {
			m.AppendDecisionFact("line")
		}
		lines := strings.Split(m.scratchpad.Decisions, "\n")
		if len(lines) > decisionsMaxLines {
			t.Errorf("Decisions lines = %d, want <= %d", len(lines), decisionsMaxLines)
		}
	})
}

func TestScratchpadManagerAppendDecisionFacts(t *testing.T) {
	m := &ScratchpadManager{}
	m.AppendDecisionFacts([]string{"a", "b", "c"})
	lines := strings.Split(m.scratchpad.Decisions, "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "a" || lines[1] != "b" || lines[2] != "c" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestScratchpadManagerSetWorkingFile(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		lastAction     string
		wantPath       string
		wantLastAction string
	}{
		{
			name:           "sets both",
			path:           "foo/bar.go",
			lastAction:     "edited",
			wantPath:       "foo/bar.go",
			wantLastAction: "edited",
		},
		{
			name:           "empty path does not overwrite",
			path:           "",
			lastAction:     "edited",
			wantPath:       "",
			wantLastAction: "edited",
		},
		{
			name:           "empty lastAction does not overwrite",
			path:           "foo.go",
			lastAction:     "",
			wantPath:       "foo.go",
			wantLastAction: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &ScratchpadManager{}
			m.SetWorkingFile(tc.path, tc.lastAction)
			if m.scratchpad.WorkingFile != tc.wantPath {
				t.Errorf("WorkingFile = %q, want %q", m.scratchpad.WorkingFile, tc.wantPath)
			}
			if m.scratchpad.LastAction != tc.wantLastAction {
				t.Errorf("LastAction = %q, want %q", m.scratchpad.LastAction, tc.wantLastAction)
			}
		})
	}
}

func TestScratchpadManagerShouldRunScaffoldInference(t *testing.T) {
	t.Run("returns false in hybrid mode", func(t *testing.T) {
		m := &ScratchpadManager{mode: config.ScratchpadModeHybrid}
		state := RunState{}
		if m.ShouldRunScaffoldInference(state, 0) {
			t.Error("want false in hybrid mode")
		}
	})

	t.Run("returns true first time in scaffold-only", func(t *testing.T) {
		m := &ScratchpadManager{mode: config.ScratchpadModeScaffoldOnly}
		state := RunState{}
		if !m.ShouldRunScaffoldInference(state, 0) {
			t.Error("want true on first call in scaffold-only mode")
		}
	})

	t.Run("deduplicates same fingerprint", func(t *testing.T) {
		m := &ScratchpadManager{mode: config.ScratchpadModeScaffoldOnly}
		state := RunState{}
		m.ShouldRunScaffoldInference(state, 0) // first: consumes
		if m.ShouldRunScaffoldInference(state, 0) {
			t.Error("want false on same fingerprint (deduplicated)")
		}
	})

	t.Run("returns true when compaction count changes", func(t *testing.T) {
		m := &ScratchpadManager{mode: config.ScratchpadModeScaffoldOnly}
		state := RunState{}
		m.ShouldRunScaffoldInference(state, 0)
		if !m.ShouldRunScaffoldInference(state, 1) {
			t.Error("want true when compaction count changes")
		}
	})
}

func TestScratchpadManagerApplyScaffoldInference(t *testing.T) {
	t.Run("valid JSON updates intent and next", func(t *testing.T) {
		m := &ScratchpadManager{}
		ok, note := m.ApplyScaffoldInference(1, `{"intent":"refactor","next":"run tests"}`)
		if !ok {
			t.Errorf("ApplyScaffoldInference() = false, note=%q", note)
		}
		if m.scratchpad.Intent != "refactor" {
			t.Errorf("Intent = %q, want refactor", m.scratchpad.Intent)
		}
		if m.scratchpad.Next != "run tests" {
			t.Errorf("Next = %q, want run tests", m.scratchpad.Next)
		}
	})

	t.Run("invalid JSON returns false with note", func(t *testing.T) {
		m := &ScratchpadManager{}
		ok, note := m.ApplyScaffoldInference(1, "bad json{{")
		if ok {
			t.Error("ApplyScaffoldInference() = true on invalid JSON, want false")
		}
		if note == "" {
			t.Error("want non-empty note on parse failure")
		}
	})

	t.Run("partial JSON still succeeds", func(t *testing.T) {
		m := &ScratchpadManager{scratchpad: Scratchpad{Intent: "original"}}
		ok, _ := m.ApplyScaffoldInference(1, `{"intent":"updated"}`)
		if !ok {
			t.Error("ApplyScaffoldInference() = false on partial JSON, want true")
		}
		if m.scratchpad.Intent != "updated" {
			t.Errorf("Intent = %q, want updated", m.scratchpad.Intent)
		}
	})
}

func TestScratchpadManagerReset(t *testing.T) {
	m := &ScratchpadManager{
		failures: 5,
		scratchpad: Scratchpad{
			Intent:      "do things",
			Decisions:   "decided x",
			Open:        "open question",
			Next:        "next step",
			WorkingFile: "foo.go",
			LastAction:  "edited",
		},
	}
	m.Reset()

	if m.failures != 0 {
		t.Errorf("failures = %d, want 0", m.failures)
	}
	if m.scratchpad.Intent != "" {
		t.Errorf("Intent = %q, want empty", m.scratchpad.Intent)
	}
	if m.scratchpad.Decisions != "" {
		t.Errorf("Decisions = %q, want empty", m.scratchpad.Decisions)
	}
	if m.scratchpad.Open != "" {
		t.Errorf("Open = %q, want empty", m.scratchpad.Open)
	}
	if m.scratchpad.Next != "" {
		t.Errorf("Next = %q, want empty", m.scratchpad.Next)
	}
	if m.scratchpad.WorkingFile != "" {
		t.Errorf("WorkingFile = %q, want empty", m.scratchpad.WorkingFile)
	}
	if m.scratchpad.LastAction != "" {
		t.Errorf("LastAction = %q, want empty", m.scratchpad.LastAction)
	}
}
