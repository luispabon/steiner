// Package quota records provider usage quota snapshots (rate-limit windows and
// spend budgets) per account, persists them as local history, and projects
// when a window or budget is likely to run out.
//
// The package is a leaf domain package. It uses only the standard library,
// golang.org/x/sys/unix, and internal/fsatomic, and it does not import provider,
// agent, tui, or config. Callers supply fetchers and passive observations; the
// package owns merging, persistence, projections, and refresh scheduling.
package quota

import (
	"context"
	"time"
)

// Source records how a snapshot was obtained.
type Source string

const (
	// SourcePassive marks a snapshot observed from ordinary provider traffic.
	SourcePassive Source = "passive"
	// SourceFetch marks a snapshot returned by a Fetcher.
	SourceFetch Source = "fetch"
)

// Window is one rate-limit window reported for an account.
type Window struct {
	Label       string        `json:"label"`
	Duration    time.Duration `json:"duration"`
	UsedPercent float64       `json:"used_percent"`
	ResetsAt    time.Time     `json:"resets_at"`
	Status      string        `json:"status"`
}

// Budget is one spend or credit budget reported for an account.
type Budget struct {
	Label    string     `json:"label"`
	Used     float64    `json:"used"`
	Limit    *float64   `json:"limit,omitempty"`
	Currency string     `json:"currency"`
	Period   string     `json:"period"`
	ResetsAt *time.Time `json:"resets_at,omitempty"`
}

// Snapshot is the quota state of one account observed at FetchedAt.
type Snapshot struct {
	AccountKey   string    `json:"account_key"`
	ProviderType string    `json:"provider_type"`
	Plan         string    `json:"plan"`
	Windows      []Window  `json:"windows"`
	Budgets      []Budget  `json:"budgets"`
	Credits      string    `json:"credits"`
	FetchedAt    time.Time `json:"fetched_at"`
	Source       Source    `json:"source"`
}

// FetchOptions controls one Fetcher call.
type FetchOptions struct {
	// AllowSpawn permits the fetcher to start helper processes. RefreshUsed
	// passes false; RefreshAll passes true.
	AllowSpawn bool
}

// Fetcher retrieves a fresh snapshot for one account.
type Fetcher interface {
	Fetch(context.Context, FetchOptions) (Snapshot, error)
}

// TimeoutFetcher is optionally implemented by a Fetcher that needs a longer or
// shorter fetch timeout than ServiceOptions.FetchTimeout. A non-positive value
// means the service timeout applies.
type TimeoutFetcher interface {
	FetchTimeout() time.Duration
}

// resetsAtTolerance is how far apart two ResetsAt values may be and still name
// the same reset. The Claude CLI repeats the same reset time with sub-second
// drift on every reply, and Codex derives resets_at from resets_in_seconds plus
// now, so request latency and whole-second rounding add a second or two. A
// genuine window or budget reset moves it by hours.
const resetsAtTolerance = 5 * time.Second

// sameState reports whether a and b carry the same semantic quota state.
// FetchedAt and Source are ignored. Reset times compare within resetsAtTolerance,
// and pointer fields compare by the values they point to.
func sameState(a, b Snapshot) bool {
	if a.AccountKey != b.AccountKey || a.ProviderType != b.ProviderType ||
		a.Plan != b.Plan || a.Credits != b.Credits ||
		len(a.Windows) != len(b.Windows) || len(a.Budgets) != len(b.Budgets) {
		return false
	}
	for i := range a.Windows {
		wa, wb := a.Windows[i], b.Windows[i]
		if wa.Label != wb.Label || wa.Duration != wb.Duration ||
			wa.UsedPercent != wb.UsedPercent || wa.Status != wb.Status ||
			!sameResetTime(wa.ResetsAt, wb.ResetsAt) {
			return false
		}
	}
	for i := range a.Budgets {
		if !sameBudget(a.Budgets[i], b.Budgets[i]) {
			return false
		}
	}
	return true
}

func sameBudget(a, b Budget) bool {
	return a.Label == b.Label && a.Used == b.Used &&
		a.Currency == b.Currency && a.Period == b.Period &&
		sameFloatPtr(a.Limit, b.Limit) && sameTimePtr(a.ResetsAt, b.ResetsAt)
}

func sameFloatPtr(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameTimePtr(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return sameResetTime(*a, *b)
}

// sameResetTime reports whether a and b name the same reset instant. Two zero
// values match, a zero value never matches a real time, and real times match
// within resetsAtTolerance.
func sameResetTime(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return a.IsZero() && b.IsZero()
	}
	return a.Sub(b).Abs() <= resetsAtTolerance
}
