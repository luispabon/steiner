package quota

import (
	"context"
	"sync"
	"testing"
	"time"
)

// testBase is the fixed clock used by every test in the package.
var testBase = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T {
	return &v
}

// sampleSnapshot returns a snapshot for key fetched at fetchedAt, with one 5h
// window at used percent and one budget. Every call builds fresh slices and
// pointers, so tests can mutate the result without affecting other snapshots.
func sampleSnapshot(key string, fetchedAt time.Time, used float64) Snapshot {
	return Snapshot{
		AccountKey:   key,
		ProviderType: "claude",
		Plan:         "pro",
		Windows: []Window{{
			Label:       "5h",
			Duration:    5 * time.Hour,
			UsedPercent: used,
			ResetsAt:    testBase.Add(5 * time.Hour),
			Status:      "allowed",
		}},
		Budgets: []Budget{{
			Label:    "extra",
			Used:     3,
			Limit:    ptr(50.0),
			Currency: "GBP",
			Period:   "monthly",
			ResetsAt: ptr(testBase.Add(30 * 24 * time.Hour)),
		}},
		FetchedAt: fetchedAt,
		Source:    SourceFetch,
	}
}

// fakeFetcher returns snap or err. When block is non-nil, Fetch waits until it
// is closed. When started is non-nil, Fetch sends one value on entry.
type fakeFetcher struct {
	snap    Snapshot
	err     error
	block   chan struct{}
	started chan struct{}

	mu        sync.Mutex
	calls     []FetchOptions
	deadlines []time.Time
}

func (f *fakeFetcher) Fetch(ctx context.Context, opts FetchOptions) (Snapshot, error) {
	f.mu.Lock()
	f.calls = append(f.calls, opts)
	if d, ok := ctx.Deadline(); ok {
		f.deadlines = append(f.deadlines, d)
	}
	f.mu.Unlock()
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.block != nil {
		<-f.block
	}
	if f.err != nil {
		return Snapshot{}, f.err
	}
	return f.snap, nil
}

func (f *fakeFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// timedFetcher is a fakeFetcher that also reports a fetch timeout.
type timedFetcher struct {
	*fakeFetcher
	timeout time.Duration
}

func (f timedFetcher) FetchTimeout() time.Duration {
	return f.timeout
}

// mustDrain fails unless the service writes every snapshot it has queued.
func mustDrain(t *testing.T, svc *Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.Drain(ctx); err != nil {
		t.Fatalf("Drain: %v", err)
	}
}
