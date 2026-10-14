package quota

import (
	"testing"
	"time"
)

func TestWindowLabel(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"zero is usage", 0, "usage"},
		{"negative is usage", -time.Hour, "usage"},
		{"5h exact", 5 * time.Hour, "5h"},
		{"5h lower tolerance bound", 4*time.Hour + 45*time.Minute, "5h"},
		{"5h upper tolerance bound", 5*time.Hour + 15*time.Minute, "5h"},
		{"outside 5h tolerance is humanised", 3*time.Hour + 10*time.Minute, "3h"},
		{"24h is daily", 24 * time.Hour, "daily"},
		{"23h is daily within tolerance", 23 * time.Hour, "daily"},
		{"7d is weekly", 7 * 24 * time.Hour, "weekly"},
		{"30d is monthly", 30 * 24 * time.Hour, "monthly"},
		{"2h humanised", 2 * time.Hour, "2h"},
		{"3d humanised", 3 * 24 * time.Hour, "3d"},
		{"45m humanised", 45 * time.Minute, "45m"},
		{"sub-minute rounds up to 1m", 10 * time.Second, "1m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WindowLabel(tt.duration); got != tt.want {
				t.Errorf("WindowLabel(%s) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}
