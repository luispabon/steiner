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

func TestStatusBarRendersExtSegment(t *testing.T) {
	tests := []struct {
		name             string
		extCurrent       int
		extMax           int
		shouldContain    string
		shouldNotContain string
	}{
		{
			name:          "ext segment present when extMax > 0",
			extCurrent:    0,
			extMax:        5,
			shouldContain: "ext ",
		},
		{
			name:          "ext segment shows correct count",
			extCurrent:    2,
			extMax:        5,
			shouldContain: "2/5",
		},
		{
			name:             "ext segment omitted when extMax is 0",
			extCurrent:       0,
			extMax:           0,
			shouldNotContain: "ext ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := statusState{extCurrent: tt.extCurrent, extMax: tt.extMax}
			result := s.view(80)
			if tt.shouldContain != "" && !strings.Contains(result, tt.shouldContain) {
				t.Errorf("status bar should contain %q, got: %s", tt.shouldContain, result)
			}
			if tt.shouldNotContain != "" && strings.Contains(result, tt.shouldNotContain) {
				t.Errorf("status bar should not contain %q, got: %s", tt.shouldNotContain, result)
			}
		})
	}
}
