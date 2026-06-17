package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
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
		fmt.Fprintf(s.w, "  %s\n", s.label)
	}
}

// Stop terminates the spinner and writes the final line. Call with success=true
// for a checkmark, success=false for a cross mark.
func (s *Spinner) Stop(success bool, final string) {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.started = false
	s.mu.Unlock()

	if s.tty {
		close(s.stopCh)
		<-s.doneCh
		// Clear line and write final
		mark := checkMark()
		if !success {
			mark = crossMark()
		}
		fmt.Fprintf(s.w, "\r  \x1b[2K%s %s\n", mark, s.style.Render(final))
	} else {
		mark := checkMark()
		if !success {
			mark = crossMark()
		}
		fmt.Fprintf(s.w, "  %s %s\n", mark, final)
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
			fmt.Fprintf(s.w, "\r  \x1b[2K%s %s", rendered, s.label)
		}
	}
}

// isTTY checks whether w is connected to a terminal that supports ANSI.
// Mirrors the logic from internal/output/plain.go (supportsANSI).
func isTTY(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		return false
	}
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
