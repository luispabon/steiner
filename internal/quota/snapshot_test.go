package quota

import (
	"testing"
	"time"
)

func TestSameState(t *testing.T) {
	base := sampleSnapshot("a", testBase, 10)
	tests := []struct {
		name   string
		change func(*Snapshot)
		want   bool
	}{
		{"identical", func(*Snapshot) {}, true},
		{"fetched at and source ignored", func(s *Snapshot) {
			s.FetchedAt = s.FetchedAt.Add(time.Hour)
			s.Source = SourcePassive
		}, true},
		{"same reset instant in another zone", func(s *Snapshot) {
			s.Windows[0].ResetsAt = s.Windows[0].ResetsAt.In(time.FixedZone("plus2", 2*3600))
		}, true},
		{"window usage differs", func(s *Snapshot) { s.Windows[0].UsedPercent = 11 }, false},
		{"window count differs", func(s *Snapshot) { s.Windows = nil }, false},
		{"plan differs", func(s *Snapshot) { s.Plan = "max" }, false},
		{"budget limit value differs", func(s *Snapshot) { *s.Budgets[0].Limit = 99 }, false},
		{"budget limit becomes nil", func(s *Snapshot) { s.Budgets[0].Limit = nil }, false},
		{"budget reset becomes nil", func(s *Snapshot) { s.Budgets[0].ResetsAt = nil }, false},
		{"budget used differs", func(s *Snapshot) { s.Budgets[0].Used = 4 }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := sampleSnapshot("a", testBase, 10)
			tt.change(&candidate)
			if got := sameState(base, candidate); got != tt.want {
				t.Errorf("sameState = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSameStateResetsAtTolerance(t *testing.T) {
	// The Claude CLI reports the same window reset with sub-second drift on every
	// reply. These two values straddle a second boundary.
	straddling := time.Date(2026, 10, 30, 20, 40, 0, 276056000, time.UTC)
	jittered := time.Date(2026, 10, 30, 20, 39, 59, 680190000, time.UTC)
	windowReset := testBase.Add(5 * time.Hour)
	budgetReset := testBase.Add(30 * 24 * time.Hour)

	tests := []struct {
		name      string
		baseReset time.Time
		candidate time.Time
		budgetAt  *time.Time
		used      float64
		want      bool
	}{
		{
			name:      "sub-second jitter across a second boundary",
			baseReset: straddling,
			candidate: jittered,
			used:      10,
			want:      true,
		},
		{
			name:      "59 seconds apart",
			baseReset: windowReset,
			candidate: windowReset.Add(-59 * time.Second),
			used:      10,
			want:      true,
		},
		{
			name:      "two minutes apart",
			baseReset: windowReset,
			candidate: windowReset.Add(2 * time.Minute),
			used:      10,
			want:      false,
		},
		{
			name:      "zero reset against a real reset",
			baseReset: windowReset,
			candidate: time.Time{},
			used:      10,
			want:      false,
		},
		{
			name:      "utilization change with equal resets",
			baseReset: windowReset,
			candidate: windowReset,
			used:      11,
			want:      false,
		},
		{
			name:      "budget reset jitter within tolerance",
			baseReset: windowReset,
			candidate: windowReset,
			budgetAt:  ptr(budgetReset.Add(30 * time.Second)),
			used:      10,
			want:      true,
		},
		{
			name:      "budget reset outside tolerance",
			baseReset: windowReset,
			candidate: windowReset,
			budgetAt:  ptr(budgetReset.Add(5 * time.Minute)),
			used:      10,
			want:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := sampleSnapshot("a", testBase, 10)
			base.Windows[0].ResetsAt = tt.baseReset
			candidate := sampleSnapshot("a", testBase, tt.used)
			candidate.Windows[0].ResetsAt = tt.candidate
			if tt.budgetAt != nil {
				candidate.Budgets[0].ResetsAt = tt.budgetAt
			}
			if got := sameState(base, candidate); got != tt.want {
				t.Errorf("sameState = %v, want %v", got, tt.want)
			}
		})
	}
}
