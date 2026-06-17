package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestSpinner_NonTTY_StaticLine(t *testing.T) {
	var buf bytes.Buffer
	sp := NewSpinner(&buf, "Downloading…")
	sp.Start()
	sp.Stop(true, "done")

	got := buf.String()
	if !strings.Contains(got, "Downloading…") {
		t.Errorf("got %q, want substring %q", got, "Downloading…")
	}
	if !strings.Contains(got, "done") {
		t.Errorf("got %q, want substring %q", got, "done")
	}
	if strings.Contains(got, "\r") {
		t.Errorf("non-TTY output should not contain \\r: %q", got)
	}
}

func TestSpinner_NonTTY_Failure(t *testing.T) {
	var buf bytes.Buffer
	sp := NewSpinner(&buf, "Working…")
	sp.Start()
	sp.Stop(false, "failed: network error")

	got := buf.String()
	if !strings.Contains(got, "failed: network error") {
		t.Errorf("got %q, want error message", got)
	}
}

func TestSpinner_StopWithoutStart(t *testing.T) {
	var buf bytes.Buffer
	sp := NewSpinner(&buf, "label")
	// Call Stop without Start — must not panic.
	sp.Stop(true, "done")
	// Output should be empty (no Start call).
	if buf.Len() != 0 {
		t.Errorf("got %q, want empty", buf.String())
	}
}

func TestSpinner_DoubleStart(t *testing.T) {
	var buf bytes.Buffer
	sp := NewSpinner(&buf, "label")
	sp.Start()
	sp.Start() // second Start must be no-op
	sp.Stop(true, "done")
	// Should have exactly one "label" line (non-TTY).
	got := buf.String()
	count := strings.Count(got, "label")
	if count != 1 {
		t.Errorf("label appears %d times, want 1: %q", count, got)
	}
}

func TestSpinner_ImmediateStop_TTY(t *testing.T) {
	// Even with a real timer, the stop channel should win immediately.
	// This test just verifies no panic for the TTY path.
	// We can't easily test TTY without an *os.File, so we only test the
	// fast-path here (we set tty=false above, but let's also test that
	// setting up the channels works via a short tick).
	// Instead, test that Start+Stop on a non-TTY buffer completes fast.
	var buf bytes.Buffer
	sp := NewSpinner(&buf, "test")
	start := time.Now()
	sp.Start()
	sp.Stop(true, "ok")
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Errorf("Stop took too long: %v", elapsed)
	}
}
