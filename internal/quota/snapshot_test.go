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
