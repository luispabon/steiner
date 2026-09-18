//go:build linux

package notify

import (
	"os/exec"
	"testing"
	"time"
)

// F275: focusTerminal must use a timeout to prevent hung window-management commands from blocking indefinitely.
func TestRunFocusCommandEnforcesTimeout(t *testing.T) {
	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep command not available")
	}

	start := time.Now()
	runFocusCommand([]string{sleepPath, "30"}, 50*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("runFocusCommand took %v, expected well under 5 seconds", elapsed)
	}
}

// F275: focusTerminal should handle nil argv gracefully.
func TestRunFocusCommandHandlesNilArgv(t *testing.T) {
	runFocusCommand(nil, 1*time.Second)
}

// F275: focusTerminal should handle non-existent command gracefully.
func TestRunFocusCommandHandlesNonExistentCommand(t *testing.T) {
	runFocusCommand([]string{"/nonexistent/command"}, 1*time.Second)
}
