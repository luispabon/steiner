package quota

import (
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

func windowAt(key string, at time.Time, used float64, reset time.Time) Snapshot {
	return Snapshot{
		AccountKey: key,
		FetchedAt:  at,
		Windows:    []Window{{Label: "5h", UsedPercent: used, ResetsAt: reset}},
	}
}

func claudeSubWindowAt(at time.Time, used float64, reset time.Time, dur time.Duration) Snapshot {
	return Snapshot{
		AccountKey:   "a",
		ProviderType: claudeSubscriptionProviderType,
		FetchedAt:    at,
		Windows:      []Window{{Label: "5h", Duration: dur, UsedPercent: used, ResetsAt: reset}},
	}
}

func budgetAt(at time.Time, used float64, limit *float64, reset *time.Time) Snapshot {
	return Snapshot{
		AccountKey: "a",
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

func TestProjectClaudeSubscriptionWindowFromStart(t *testing.T) {
	const full = 5 * time.Hour
	// resetFor returns the reset time of a five-hour window that started
	// elapsed before testBase.
	resetFor := func(elapsed time.Duration) time.Time { return testBase.Add(full - elapsed) }
	otherProvider := claudeSubWindowAt(testBase, 27, resetFor(40*time.Minute), full)
	otherProvider.ProviderType = "anthropic"

	tests := []struct {
		name            string
		history         []Snapshot
		current         Snapshot
		wantOK          bool
		wantEmptyIn     time.Duration
		wantBeforeReset bool
	}{
		{
			name:            "average forecasts when history alone is below threshold",
			history:         []Snapshot{windowAt("a", testBase.Add(-30*time.Minute), 26.5, resetFor(40*time.Minute))},
			current:         claudeSubWindowAt(testBase, 27, resetFor(40*time.Minute), full),
			wantOK:          true,
			wantEmptyIn:     108 * time.Minute, // 40m elapsed at 27%: 40m * 73 / 27.
			wantBeforeReset: true,
		},
		{
			name:            "average forecasts without history",
			current:         claudeSubWindowAt(testBase, 27, resetFor(40*time.Minute), full),
			wantOK:          true,
			wantEmptyIn:     108 * time.Minute,
			wantBeforeReset: true,
		},
		{
			name: "average overrides history that projects differently",
			history: []Snapshot{
				windowAt("a", testBase.Add(-30*time.Minute), 5, resetFor(40*time.Minute)),
				windowAt("a", testBase.Add(-10*time.Minute), 20, resetFor(40*time.Minute)),
			},
			current:         claudeSubWindowAt(testBase, 27, resetFor(40*time.Minute), full),
			wantOK:          true,
			wantEmptyIn:     108 * time.Minute, // History would project 99.5m ahead.
			wantBeforeReset: true,
		},
		{
			name:            "average forecasts past the reset",
			current:         claudeSubWindowAt(testBase, 10, resetFor(40*time.Minute), full),
			wantOK:          true,
			wantEmptyIn:     360 * time.Minute, // 40m elapsed at 10%: 40m * 90 / 10, reset in 260m.
			wantBeforeReset: false,
		},
		{
			name:    "span under 15m after the window start is rejected",
			current: claudeSubWindowAt(testBase, 27, resetFor(5*time.Minute), full),
		},
		{
			name:    "usage under 2 percent after the window start is rejected",
			current: claudeSubWindowAt(testBase, 1, resetFor(40*time.Minute), full),
		},
		{
			name:            "exact minimum span after the window start is accepted",
			current:         claudeSubWindowAt(testBase, 50, resetFor(15*time.Minute), full),
			wantOK:          true,
			wantEmptyIn:     15 * time.Minute, // 15m elapsed at 50%: 15m * 50 / 50.
			wantBeforeReset: true,
		},
		{
			name:            "exact minimum rise after the window start is accepted",
			current:         claudeSubWindowAt(testBase, 2, resetFor(40*time.Minute), full),
			wantOK:          true,
			wantEmptyIn:     1960 * time.Minute, // 40m elapsed at 2%: 40m * 98 / 2, reset in 260m.
			wantBeforeReset: false,
		},
		{
			name:    "zero usage after the window start projects nothing",
			current: claudeSubWindowAt(testBase, 0, resetFor(40*time.Minute), full),
		},
		{
			name:    "negative usage after the window start projects nothing",
			current: claudeSubWindowAt(testBase, -5, resetFor(40*time.Minute), full),
		},
		{
			name:            "unusable average falls back to history",
			history:         []Snapshot{windowAt("a", testBase.Add(-30*time.Minute), 10, resetFor(5*time.Minute))},
			current:         claudeSubWindowAt(testBase, 20, resetFor(5*time.Minute), full),
			wantOK:          true,
			wantEmptyIn:     240 * time.Minute,
			wantBeforeReset: true,
		},
		{
			name:            "sample before the window start falls back to history",
			history:         []Snapshot{windowAt("a", testBase.Add(-30*time.Minute), 10, testBase.Add(full+10*time.Minute))},
			current:         claudeSubWindowAt(testBase, 20, testBase.Add(full+10*time.Minute), full),
			wantOK:          true,
			wantEmptyIn:     240 * time.Minute,
			wantBeforeReset: true,
		},
		{
			name:            "sample at the window start falls back to history",
			history:         []Snapshot{windowAt("a", testBase.Add(-30*time.Minute), 10, testBase.Add(full))},
			current:         claudeSubWindowAt(testBase, 20, testBase.Add(full), full),
			wantOK:          true,
			wantEmptyIn:     240 * time.Minute, // History: 30m rose 10 points, 80 points left.
			wantBeforeReset: true,
		},
		{
			name:    "sample at the reset projects nothing",
			history: []Snapshot{windowAt("a", testBase.Add(-30*time.Minute), 10, testBase)},
			current: claudeSubWindowAt(testBase, 20, testBase, full),
		},
		{
			name:    "sample after the reset projects nothing",
			history: []Snapshot{windowAt("a", testBase.Add(-30*time.Minute), 10, testBase.Add(-5*time.Minute))},
			current: claudeSubWindowAt(testBase, 20, testBase.Add(-5*time.Minute), full),
		},
		{
			name:    "other provider falls back to history",
			current: otherProvider,
		},
		{
			name:            "seven-day window falls back to history",
			history:         []Snapshot{windowAt("a", testBase.Add(-30*time.Minute), 10, testBase.Add(6*24*time.Hour))},
			current:         claudeSubWindowAt(testBase, 20, testBase.Add(6*24*time.Hour), 7*24*time.Hour),
			wantOK:          true,
			wantEmptyIn:     240 * time.Minute,
			wantBeforeReset: true,
		},
		{
			name:            "zero duration falls back to history",
			history:         []Snapshot{windowAt("a", testBase.Add(-30*time.Minute), 10, resetFor(40*time.Minute))},
			current:         claudeSubWindowAt(testBase, 20, resetFor(40*time.Minute), 0),
			wantOK:          true,
			wantEmptyIn:     240 * time.Minute,
			wantBeforeReset: true,
		},
		{
			name:            "full usage is empty now",
			current:         claudeSubWindowAt(testBase, 100, resetFor(40*time.Minute), full),
			wantOK:          true,
			wantEmptyIn:     0,
			wantBeforeReset: true,
		},
		{
			name:            "usage over capacity is empty now",
			current:         claudeSubWindowAt(testBase, 120, resetFor(40*time.Minute), full),
			wantOK:          true,
			wantEmptyIn:     0,
			wantBeforeReset: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProjectWindow(tt.history, tt.current, 0)
			if !tt.wantOK {
				checkProjection(t, got, false, time.Time{}, false)
				return
			}
			if !got.OK {
				t.Fatalf("Projection = %+v, want OK", got)
			}
			if got.BeforeReset != tt.wantBeforeReset {
				t.Errorf("BeforeReset = %v, want %v", got.BeforeReset, tt.wantBeforeReset)
			}
			want := tt.current.FetchedAt.Add(tt.wantEmptyIn)
			if diff := got.EmptyAt.Sub(want); diff > time.Minute || diff < -time.Minute {
				t.Errorf("EmptyAt = %s, want within a minute of %s", got.EmptyAt, want)
			}
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
			history:         []Snapshot{budgetAt(testBase, 10, limit, reset)},
			current:         budgetAt(testBase.Add(20*time.Minute), 12, limit, reset),
			wantOK:          true,
			wantEmptyAt:     testBase.Add(900 * time.Minute),
			wantBeforeReset: true,
		},
		{
			name:    "rise under 2 percent of limit is rejected",
			history: []Snapshot{budgetAt(testBase, 10, limit, reset)},
			current: budgetAt(testBase.Add(20*time.Minute), 11.9, limit, reset),
		},
		{
			name:    "span under 15m is rejected",
			history: []Snapshot{budgetAt(testBase, 10, limit, reset)},
			current: budgetAt(testBase.Add(14*time.Minute), 12, limit, reset),
		},
		{
			name:    "budget without limit is not projected",
			history: []Snapshot{budgetAt(testBase, 10, nil, reset)},
			current: budgetAt(testBase.Add(20*time.Minute), 12, nil, reset),
		},
		{
			name:    "zero limit is not projected",
			history: []Snapshot{budgetAt(testBase, 10, ptr(0.0), reset)},
			current: budgetAt(testBase.Add(20*time.Minute), 12, ptr(0.0), reset),
		},
		{
			name:    "budget without reset is not projected",
			history: []Snapshot{budgetAt(testBase, 10, limit, reset)},
			current: budgetAt(testBase.Add(20*time.Minute), 12, limit, nil),
		},
		{
			name:    "reset moved by more than a minute is rejected",
			history: []Snapshot{budgetAt(testBase, 10, limit, ptr(reset.Add(time.Hour)))},
			current: budgetAt(testBase.Add(20*time.Minute), 12, limit, reset),
		},
		{
			name: "budget label must match",
			history: []Snapshot{{
				AccountKey: "a",
				FetchedAt:  testBase,
				Budgets:    []Budget{{Label: "other", Used: 10, Limit: limit, ResetsAt: reset}},
			}},
			current: budgetAt(testBase.Add(20*time.Minute), 12, limit, reset),
		},
		{
			name:    "budget index out of range",
			history: []Snapshot{budgetAt(testBase, 10, limit, reset)},
			current: budgetAt(testBase.Add(20*time.Minute), 12, limit, reset),
			index:   1,
		},
		{
			name:            "empty after reset is not before reset",
			history:         []Snapshot{budgetAt(testBase, 10, limit, ptr(testBase.Add(time.Hour)))},
			current:         budgetAt(testBase.Add(20*time.Minute), 12, limit, ptr(testBase.Add(time.Hour))),
			wantOK:          true,
			wantEmptyAt:     testBase.Add(900 * time.Minute),
			wantBeforeReset: false,
		},
		{
			name:            "usage at limit is empty now",
			history:         []Snapshot{budgetAt(testBase, 90, limit, reset)},
			current:         budgetAt(testBase.Add(20*time.Minute), 100, limit, reset),
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

// TestClaudeSubscriptionProviderTypeMatchesConfig pins the provider literal
// duplicated in projection.go to the config constant it mirrors.
func TestClaudeSubscriptionProviderTypeMatchesConfig(t *testing.T) {
	if claudeSubscriptionProviderType != string(config.ProviderTypeClaudeSubscription) {
		t.Errorf("claudeSubscriptionProviderType = %q, config.ProviderTypeClaudeSubscription = %q", claudeSubscriptionProviderType, config.ProviderTypeClaudeSubscription)
	}
}
