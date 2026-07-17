package main

import (
	"fmt"
	"io"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
)

// Spinner wraps a bubbles spinner with a Start/Stop API and TTY auto-detection.
type Spinner struct {
	w       io.Writer
	label   string
	style   lipgloss.Style
	ticker  *time.Ticker
	stopCh  chan struct{}
	doneCh  chan struct{}
	tty     bool
	mu      sync.Mutex
	started bool
}

// NewSpinner creates a new spinner that writes to w. label is displayed
// alongside the spinning animation.
func NewSpinner(w io.Writer, label string) *Spinner {
	accent := accentColor()
	style := lipgloss.NewStyle().Foreground(accent).Bold(true)
	return &Spinner{
		w:      w,
		label:  label,
		style:  style,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
		tty:    isTTY(w),
	}
}

// Start begins the spinner animation. In TTY mode it launches a background
// goroutine. In non-TTY mode it writes a single static line.
func (s *Spinner) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return
	}
	s.started = true
	if s.tty {
		s.ticker = time.NewTicker(spinner.Dot.FPS)
		go s.run()
	} else {
		// Non-TTY: write one static line
		_, _ = fmt.Fprintf(s.w, "  %s\n", s.label)
	}
}

// markStopped marks the spinner as no longer running, returning false if it
// was already stopped.
func (s *Spinner) markStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return false
	}
	s.started = false
	return true
}

// Stop terminates the spinner and writes the final line. Call with success=true
// for a checkmark, success=false for a cross mark.
func (s *Spinner) Stop(success bool, final string) {
	if !s.markStopped() {
		return
	}

	if s.tty {
		close(s.stopCh)
		<-s.doneCh
		s.ticker.Stop()
		// Clear line and write final
		mark := checkMark()
		if !success {
			mark = crossMark()
		}
		_, _ = fmt.Fprintf(s.w, "\r  \x1b[2K%s %s\n", mark, s.style.Render(final))
	} else {
		mark := checkMark()
		if !success {
			mark = crossMark()
		}
		_, _ = fmt.Fprintf(s.w, "  %s %s\n", mark, final)
	}
}

// Clear stops the spinner and clears the current line.
// In TTY mode, it writes a clear escape sequence. In non-TTY mode it is a
// no-op. Safe to call when not started.
func (s *Spinner) Clear() {
	if !s.markStopped() {
		return
	}

	if s.tty {
		close(s.stopCh)
		<-s.doneCh
		s.ticker.Stop()
		_, _ = fmt.Fprintf(s.w, "\r\x1b[2K")
	}
}

func (s *Spinner) run() {
	defer close(s.doneCh)
	frames := spinner.Dot.Frames
	idx := 0
	for {
		select {
		case <-s.stopCh:
			return
		case <-s.ticker.C:
			frame := frames[idx]
			idx = (idx + 1) % len(frames)
			rendered := s.style.Render(frame)
			_, _ = fmt.Fprintf(s.w, "\r  \x1b[2K%s %s", rendered, s.label)
		}
	}
}

// isTTY checks whether w is connected to a terminal that supports ANSI.
// Mirrors the logic from internal/output/plain.go (supportsANSI).
func isTTY(w io.Writer) bool {
	return output.SupportsANSI(w)
}
