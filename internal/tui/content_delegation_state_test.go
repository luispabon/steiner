package tui

import "testing"

func TestFormatCountdown(t *testing.T) {
	tests := []struct {
		name     string
		deadline int64
		now      int64
		want     string
	}{
		{name: "fractional seconds", deadline: 9_600_000_000, now: 0, want: "9.6s"},
		{name: "expired", deadline: 0, now: 5_000_000_000, want: "0.0s"},
		{name: "midpoint", deadline: 2_800_000_000, now: 1_500_000_000, want: "1.3s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatCountdown(tt.deadline, tt.now); got != tt.want {
				t.Errorf("formatCountdown(%d, %d) = %q, want %q", tt.deadline, tt.now, got, tt.want)
			}
		})
	}
}
