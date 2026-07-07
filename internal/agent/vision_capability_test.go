package agent

import (
	"testing"
)

func TestVisionCapabilities_LatchIncapable(t *testing.T) {
	cap := NewVisionCapabilities(false)

	// First latch should return changed=true
	if changed := cap.LatchIncapable("gpt-4"); !changed {
		t.Errorf("first LatchIncapable returned changed=false, want true")
	}
	if state := cap.Get("gpt-4"); state != VisionIncapable {
		t.Errorf("Get returned %v, want VisionIncapable", state)
	}

	// Second latch should return changed=false
	if changed := cap.LatchIncapable("gpt-4"); changed {
		t.Errorf("second LatchIncapable returned changed=true, want false")
	}
}

func TestVisionCapabilities_SetDerived(t *testing.T) {
	cap := NewVisionCapabilities(false)

	// Set initial capability
	cap.SetDerived("gpt-4", VisionCapable)
	if state := cap.Get("gpt-4"); state != VisionCapable {
		t.Errorf("Get returned %v, want VisionCapable", state)
	}

	// Update Unknown to Capable
	cap2 := NewVisionCapabilities(false)
	cap2.SetDerived("gpt-4", VisionUnknown)
	cap2.SetDerived("gpt-4", VisionCapable)
	if state := cap2.Get("gpt-4"); state != VisionCapable {
		t.Errorf("Get returned %v, want VisionCapable", state)
	}

	// Latch to incapable, then try to overwrite with SetDerived
	cap3 := NewVisionCapabilities(false)
	cap3.LatchIncapable("gpt-4")
	cap3.SetDerived("gpt-4", VisionCapable)
	if state := cap3.Get("gpt-4"); state != VisionIncapable {
		t.Errorf("Get returned %v, want VisionIncapable (latch should prevent override)", state)
	}
}

func TestVisionCapabilities_TakeNotify(t *testing.T) {
	cap := NewVisionCapabilities(false)

	// First call returns true
	if notified := cap.TakeNotify("gpt-4"); !notified {
		t.Errorf("first TakeNotify returned false, want true")
	}

	// Second call returns false
	if notified := cap.TakeNotify("gpt-4"); notified {
		t.Errorf("second TakeNotify returned true, want false")
	}

	// Different alias returns true
	if notified := cap.TakeNotify("gpt-3.5"); !notified {
		t.Errorf("TakeNotify for different alias returned false, want true")
	}
}

func TestVisionCapabilities_SubAgentConfigured(t *testing.T) {
	cap1 := NewVisionCapabilities(false)
	if cap1.SubAgentConfigured() {
		t.Errorf("SubAgentConfigured returned true, want false")
	}

	cap2 := NewVisionCapabilities(true)
	if !cap2.SubAgentConfigured() {
		t.Errorf("SubAgentConfigured returned false, want true")
	}
}

func TestVisionStateFromPtr(t *testing.T) {
	tests := []struct {
		name     string
		input    *bool
		expected VisionState
	}{
		{
			name:     "nil pointer",
			input:    nil,
			expected: VisionUnknown,
		},
		{
			name:     "false pointer",
			input:    ptrBool(false),
			expected: VisionIncapable,
		},
		{
			name:     "true pointer",
			input:    ptrBool(true),
			expected: VisionCapable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VisionStateFromPtr(tt.input)
			if got != tt.expected {
				t.Errorf("VisionStateFromPtr returned %v, want %v", got, tt.expected)
			}
		})
	}
}
