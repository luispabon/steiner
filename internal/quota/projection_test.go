package quota

import (
	"testing"
	"time"
)

func windowAt(key string, at time.Time, used float64, reset time.Time) Snapshot {
	return Snapshot{
		AccountKey: key,
		FetchedAt:  at,
		Windows:    []Window{{Label: "5h", UsedPercent: used, ResetsAt: reset}},
	}
}

func budgetAt(key string, at time.Time, used float64, limit *float64, reset *time.Time) Snapshot {
	return Snapshot{
		AccountKey: key,
		FetchedAt:  at,
		Budgets:    []Budget{{Label: "extra", Used: used, Limit: limit, ResetsAt: reset}},
	}
}

func checkProjection(t *testing.T, got Projection, wantOK bool, wantEmptyAt time.Time, wantBeforeReset bool) {
	t.Helper()
	if got.OK != wantOK {
		t.Fatalf("OK = %v, want %v", got.OK, wantOK)
	}
	if !wantOK {
		if !got.EmptyAt.IsZero() || got.BeforeReset {
			t.Errorf("projection without OK = %+v, want zero value", got)
		}
		return
	}
	if !got.EmptyAt.Equal(wantEmptyAt) {
		t.Errorf("EmptyAt = %s, want %s", got.EmptyAt, wantEmptyAt)
	}
	if got.BeforeReset != wantBeforeReset {
		t.Errorf("BeforeReset = %v, want %v", got.BeforeReset, wantBeforeReset)
	}
}

func TestProjectWindow(t *testing.T) {
	reset := testBase.Add(5 * time.Hour)
	tests := []struct {
		name            string
		history         []Snapshot
		current         Snapshot
		index           int
		wantOK          bool
		wantEmptyAt     time.Time
		wantBeforeReset bool
	}{
		{
			name:            "projects before reset",
			history:         []Snapshot{windowAt("a", testBase, 10, reset)},
			current:         windowAt("a", testBase.Add(30*time.Minute), 20, reset),
			wantOK:          true,
			wantEmptyAt:     testBase.Add(270 * time.Minute),
			wantBeforeReset: true,
		},
		{
			name:            "exact 15m span and 2 point rise are accepted",
			history:         []Snapshot{windowAt("a", testBase, 10, reset)},
			current:         windowAt("a", testBase.Add(15*time.Minute), 12, reset),
			wantOK:          true,
			wantEmptyAt:     testBase.Add(675 * time.Minute),
			wantBeforeReset: false,
		},
		{
			name:    "span under 15m is rejected",
			history: []Snapshot{windowAt("a", testBase, 10, reset)},
			current: windowAt("a", testBase.Add(15*time.Minute-time.Second), 12, reset),
		},
		{
			name:    "rise under 2 points is rejected",
			history: []Snapshot{windowAt("a", testBase, 10, reset)},
			current: windowAt("a", testBase.Add(30*time.Minute), 11.9, reset),
		},
		{
			name:    "reset moved by more than a minute is rejected",
			history: []Snapshot{windowAt("a", testBase, 10, reset.Add(2*time.Minute))},
			current: windowAt("a", testBase.Add(30*time.Minute), 20, reset),
		},
		{
			name:            "reset within a minute matches",
			history:         []Snapshot{windowAt("a", testBase, 10, reset.Add(-30*time.Second))},
			current:         windowAt("a", testBase.Add(30*time.Minute), 20, reset),
			wantOK:          true,
			wantEmptyAt:     testBase.Add(270 * time.Minute),
			wantBeforeReset: true,
		},
		{
			name:    "history for another account is ignored",
			history: []Snapshot{windowAt("b", testBase, 10, reset)},
			current: windowAt("a", testBase.Add(30*time.Minute), 20, reset),
		},
		{
			name:    "no history",
			current: windowAt("a", testBase.Add(30*time.Minute), 20, reset),
		},
		{
			name:    "window index out of range",
			history: []Snapshot{windowAt("a", testBase, 10, reset)},
			current: windowAt("a", testBase.Add(30*time.Minute), 20, reset),
			index:   1,
		},
		{
			name:    "window without reset is not projected",
			history: []Snapshot{windowAt("a", testBase, 10, time.Time{})},
			current: windowAt("a", testBase.Add(30*time.Minute), 20, time.Time{}),
		},
		{
			name: "oldest matching point is used",
			history: []Snapshot{
				windowAt("a", testBase, 0, reset),
				windowAt("a", testBase.Add(15*time.Minute), 10, reset),
			},
			current:         windowAt("a", testBase.Add(30*time.Minute), 12, reset),
			wantOK:          true,
			wantEmptyAt:     testBase.Add(250 * time.Minute),
			wantBeforeReset: true,
		},
		{
			name:            "usage at capacity is empty now",
			history:         []Snapshot{windowAt("a", testBase, 90, reset)},
			current:         windowAt("a", testBase.Add(30*time.Minute), 100, reset),
			wantOK:          true,
			wantEmptyAt:     testBase.Add(30 * time.Minute),
			wantBeforeReset: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProjectWindow(tt.history, tt.current, tt.index)
			checkProjection(t, got, tt.wantOK, tt.wantEmptyAt, tt.wantBeforeReset)
		})
	}
}

func TestProjectBudget(t *testing.T) {
	limit := ptr(100.0)
	reset := ptr(testBase.Add(30 * 24 * time.Hour))
	tests := []struct {
		name            string
		history         []Snapshot
		current         Snapshot
		index           int
		wantOK          bool
		wantEmptyAt     time.Time
		wantBeforeReset bool
	}{
		{
			name:            "projects before reset",
			history:         []Snapshot{budgetAt("a", testBase, 10, limit, reset)},
			current:         budgetAt("a", testBase.Add(20*time.Minute), 12, limit, reset),
			wantOK:          true,
			wantEmptyAt:     testBase.Add(900 * time.Minute),
			wantBeforeReset: true,
		},
		{
			name:    "rise under 2 percent of limit is rejected",
			history: []Snapshot{budgetAt("a", testBase, 10, limit, reset)},
			current: budgetAt("a", testBase.Add(20*time.Minute), 11.9, limit, reset),
		},
		{
			name:    "span under 15m is rejected",
			history: []Snapshot{budgetAt("a", testBase, 10, limit, reset)},
			current: budgetAt("a", testBase.Add(14*time.Minute), 12, limit, reset),
		},
		{
			name:    "budget without limit is not projected",
			history: []Snapshot{budgetAt("a", testBase, 10, nil, reset)},
			current: budgetAt("a", testBase.Add(20*time.Minute), 12, nil, reset),
		},
		{
			name:    "zero limit is not projected",
			history: []Snapshot{budgetAt("a", testBase, 10, ptr(0.0), reset)},
			current: budgetAt("a", testBase.Add(20*time.Minute), 12, ptr(0.0), reset),
		},
		{
			name:    "budget without reset is not projected",
			history: []Snapshot{budgetAt("a", testBase, 10, limit, reset)},
			current: budgetAt("a", testBase.Add(20*time.Minute), 12, limit, nil),
		},
		{
			name:    "reset moved by more than a minute is rejected",
			history: []Snapshot{budgetAt("a", testBase, 10, limit, ptr(reset.Add(time.Hour)))},
			current: budgetAt("a", testBase.Add(20*time.Minute), 12, limit, reset),
		},
		{
			name: "budget label must match",
			history: []Snapshot{{
				AccountKey: "a",
				FetchedAt:  testBase,
				Budgets:    []Budget{{Label: "other", Used: 10, Limit: limit, ResetsAt: reset}},
			}},
			current: budgetAt("a", testBase.Add(20*time.Minute), 12, limit, reset),
		},
		{
			name:    "budget index out of range",
			history: []Snapshot{budgetAt("a", testBase, 10, limit, reset)},
			current: budgetAt("a", testBase.Add(20*time.Minute), 12, limit, reset),
			index:   1,
		},
		{
			name:            "empty after reset is not before reset",
			history:         []Snapshot{budgetAt("a", testBase, 10, limit, ptr(testBase.Add(time.Hour)))},
			current:         budgetAt("a", testBase.Add(20*time.Minute), 12, limit, ptr(testBase.Add(time.Hour))),
			wantOK:          true,
			wantEmptyAt:     testBase.Add(900 * time.Minute),
			wantBeforeReset: false,
		},
		{
			name:            "usage at limit is empty now",
			history:         []Snapshot{budgetAt("a", testBase, 90, limit, reset)},
			current:         budgetAt("a", testBase.Add(20*time.Minute), 100, limit, reset),
			wantOK:          true,
			wantEmptyAt:     testBase.Add(20 * time.Minute),
			wantBeforeReset: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProjectBudget(tt.history, tt.current, tt.index)
			checkProjection(t, got, tt.wantOK, tt.wantEmptyAt, tt.wantBeforeReset)
		})
	}
}
