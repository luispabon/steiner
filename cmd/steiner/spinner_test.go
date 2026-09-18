package main

import (
	"bytes"
	"os"
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
	// Force TTY detection on with a character-device writer (the same fixture
	// TestSupportsANSIWithCharDevice uses) and a colour-capable environment, so
	// the TTY goroutine, ticker, stop channel, and ANSI cleanup path all run.
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm")

	f, err := os.Open("/dev/null")
	if err != nil {
		t.Skipf("cannot open /dev/null: %v", err)
	}
	defer func() { _ = f.Close() }()

	if !isTTY(f) {
		t.Fatal("test writer is not detected as a TTY; cannot exercise the TTY path")
	}

	sp := NewSpinner(f, "test")
	start := time.Now()
	sp.Start()
	sp.Stop(true, "ok")
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Errorf("TTY Stop took too long: %v", elapsed)
	}
}
