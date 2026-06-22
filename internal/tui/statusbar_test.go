package tui

import (
	"strings"
	"testing"
)

func TestStatusBarRendersPhaseSegment(t *testing.T) {
	s := statusState{oneshotPhase: "plan"}
	result := s.view(80)
	if !strings.Contains(result, "phase") || !strings.Contains(result, "plan") {
		t.Errorf("status bar should contain 'phase' and 'plan', got: %s", result)
	}
}

func TestStatusBarOmitsPhaseWhenEmpty(t *testing.T) {
	s := statusState{oneshotPhase: ""}
	result := s.view(80)
	if strings.Contains(result, "phase ·") {
		t.Errorf("status bar should not contain 'phase ·', got: %s", result)
	}
}
