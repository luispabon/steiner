package quota

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestService returns a Service whose clock is a variable the test controls
// through the returned pointer. The service is drained when the test ends, so
// the asynchronous writer never runs past it.
func newTestService(t *testing.T, store *Store) (*Service, *time.Time) {
	t.Helper()
	clock := testBase
	svc := NewService(ServiceOptions{
		Store: store,
		Now:   func() time.Time { return clock },
		Logf:  t.Logf,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := svc.Drain(ctx); err != nil {
			t.Errorf("drain quota service: %v", err)
		}
	})
	return svc, &clock
}

// accountKeyForAlias returns the account key bound to alias. It reads the alias
// table under the service lock.
func accountKeyForAlias(svc *Service, alias string) (string, bool) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	key, ok := svc.aliases[alias]
	return key, ok
}

func TestNilServiceIsNoOp(t *testing.T) {
	var svc *Service
	calls := map[string]func(){
		"Observe":  func() { svc.Observe(sampleSnapshot("a", testBase, 10)) },
		"MarkUsed": func() { svc.MarkUsed("a") },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("nil Service %s panicked: %v", name, r)
				}
			}()
			call()
		})
	}
}

func TestObserveIgnoresEmptyAccountKey(t *testing.T) {
	svc, _ := newTestService(t, nil)
	svc.Observe(Snapshot{})
	if views := svc.Views(""); len(views) != 0 {
		t.Errorf("Views after empty-key Observe = %d, want 0", len(views))
	}
}

func TestRegisterMergesAliasesAndSortsViews(t *testing.T) {
	svc, _ := newTestService(t, nil)
	svc.Register(Account{Key: "k1", ProviderType: "claude", Aliases: []string{"zeta"}})
	svc.Register(Account{Key: "k2", Aliases: []string{"beta"}})
	svc.Register(Account{Key: "k1", Aliases: []string{"alpha", "zeta"}})
	svc.Register(Account{Key: "k3", Aliases: []string{"k3"}})
	svc.Observe(sampleSnapshot("k3", testBase, 1))

	if key, ok := accountKeyForAlias(svc, "alpha"); !ok || key != "k1" {
		t.Errorf("accountKeyForAlias(alpha) = %q, %v; want k1, true", key, ok)
	}
	if key, ok := accountKeyForAlias(svc, "zeta"); !ok || key != "k1" {
		t.Errorf("accountKeyForAlias(zeta) = %q, %v; want k1, true", key, ok)
	}
	if _, ok := accountKeyForAlias(svc, "missing"); ok {
		t.Error("accountKeyForAlias(missing) resolved, want unresolved")
	}

	views := svc.Views("beta")
	wantKeys := []string{"k1", "k2", "k3"}
	wantAliases := []string{"alpha", "beta", "k3"}
	if len(views) != len(wantKeys) {
		t.Fatalf("Views returned %d accounts, want %d", len(views), len(wantKeys))
	}
	for i, v := range views {
		if v.Key != wantKeys[i] || v.DisplayAlias != wantAliases[i] {
			t.Errorf("views[%d] = %s (%s), want %s (%s)", i, v.Key, v.DisplayAlias, wantKeys[i], wantAliases[i])
		}
		if wantActive := v.Key == "k2"; v.Active != wantActive {
			t.Errorf("views[%d].Active = %v, want %v", i, v.Active, wantActive)
		}
	}
	if views[0].ProviderType != "claude" {
		t.Errorf("merged account ProviderType = %q, want claude", views[0].ProviderType)
	}
	if views[2].Latest == nil {
		t.Error("registered passive account has no Latest in its view")
	}
}

func TestViewsLoadStoreOnFirstUse(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "quota-history.json"))
	if err := store.Append(sampleSnapshot("k1", testBase, 10), testBase); err != nil {
		t.Fatalf("seed Append: %v", err)
	}
	svc, _ := newTestService(t, store)
	svc.Register(Account{Key: "k1", Aliases: []string{"a"}})

	// A partial passive observation must merge into the stored state.
	svc.Observe(Snapshot{AccountKey: "k1", Windows: []Window{win("5h", 20)}, FetchedAt: testBase.Add(time.Minute)})

	views := svc.Views("")
	if len(views) != 1 || views[0].Latest == nil {
		t.Fatalf("Views = %+v, want one view with a Latest snapshot", views)
	}
	latest := views[0].Latest
	if latest.Plan != "pro" {
		t.Errorf("Plan = %q, want stored value pro", latest.Plan)
	}
	if len(latest.Budgets) != 1 {
		t.Errorf("Budgets = %d, want stored budget retained", len(latest.Budgets))
	}
	if len(latest.Windows) != 1 || latest.Windows[0].UsedPercent != 20 {
		t.Errorf("Windows = %+v, want 5h at 20 from the observation", latest.Windows)
	}
	if latest.Source != SourcePassive {
		t.Errorf("Source = %q, want passive", latest.Source)
	}
}

func TestObserveDedupesIdenticalStateAndNotifies(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "quota-history.json"))
	svc, clock := newTestService(t, store)
	svc.Register(Account{Key: "k1", Aliases: []string{"a"}})

	svc.Observe(sampleSnapshot("k1", testBase, 10))
	<-svc.Updates()

	*clock = testBase.Add(30 * time.Minute)
	svc.Observe(sampleSnapshot("k1", testBase.Add(30*time.Minute), 10))
	select {
	case <-svc.Updates():
	default:
		t.Error("identical observation did not notify")
	}
	mustDrain(t, svc)
	if history, _ := historyOf(store, "k1"); len(history) != 1 {
		t.Errorf("stored %d snapshots after identical observation, want 1", len(history))
	}
	if views := svc.Views(""); !views[0].Latest.FetchedAt.Equal(testBase.Add(30 * time.Minute)) {
		t.Errorf("in-memory FetchedAt = %s, want observation time", views[0].Latest.FetchedAt)
	}

	*clock = testBase.Add(45 * time.Minute)
	svc.Observe(sampleSnapshot("k1", testBase.Add(45*time.Minute), 20))
	mustDrain(t, svc)
	if history, _ := historyOf(store, "k1"); len(history) != 2 {
		t.Errorf("stored %d snapshots after changed observation, want 2", len(history))
	}
}

func TestUpdatesCoalesceWhileUnread(t *testing.T) {
	svc, _ := newTestService(t, nil)
	svc.Observe(sampleSnapshot("k1", testBase, 10))
	svc.Observe(sampleSnapshot("k1", testBase, 20))
	if got := len(svc.Updates()); got != 1 {
		t.Errorf("pending updates = %d, want 1 (coalesced)", got)
	}
}

func TestRefreshAllFetchesAccountsWithFetchers(t *testing.T) {
	svc, _ := newTestService(t, nil)
	f := &fakeFetcher{snap: sampleSnapshot("k1", testBase.Add(time.Hour), 50)}
	svc.Register(Account{Key: "k1", Aliases: []string{"a"}, Fetcher: f})
	svc.Register(Account{Key: "k2", Aliases: []string{"b"}})
	// RefreshAll ignores snapshot age, so a fresh observation still fetches.
	svc.Observe(sampleSnapshot("k1", testBase, 10))

	svc.RefreshAll(context.Background())

	if got := f.callCount(); got != 1 {
		t.Fatalf("fetch calls = %d, want 1", got)
	}
	if !f.calls[0].AllowSpawn {
		t.Error("RefreshAll fetch had AllowSpawn false, want true")
	}
	views := svc.Views("")
	if len(views) != 2 {
		t.Fatalf("Views = %d accounts, want 2", len(views))
	}
	v := views[0]
	if v.Latest == nil || v.Latest.Source != SourceFetch || v.Latest.Windows[0].UsedPercent != 50 {
		t.Errorf("fetched view Latest = %+v, want fetch source at 50%%", v.Latest)
	}
	if v.Latest.AccountKey != "k1" || v.Latest.ProviderType != "claude" {
		t.Errorf("fetched snapshot key/provider = %q/%q, want k1/claude", v.Latest.AccountKey, v.Latest.ProviderType)
	}
	if views[1].Latest != nil {
		t.Errorf("account without Fetcher gained a Latest snapshot")
	}
}

func TestRefreshUsedHonoursAgeAndUsedSet(t *testing.T) {
	tests := []struct {
		name       string
		observe    bool
		observedAt time.Duration // offset from testBase of the observation
		wantCalls  int
	}{
		{"no snapshot yet is fetched", false, 0, 1},
		{"fresh snapshot is not fetched", true, 0, 0},
		{"snapshot exactly minAge old is not fetched", true, -5 * time.Minute, 0},
		{"older snapshot is fetched", true, -6 * time.Minute, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService(t, nil)
			f := &fakeFetcher{snap: sampleSnapshot("k", testBase, 50)}
			svc.Register(Account{Key: "k", Aliases: []string{"a"}, Fetcher: f})
			if tt.observe {
				svc.Observe(sampleSnapshot("k", testBase.Add(tt.observedAt), 10))
			}
			svc.MarkUsed("a")

			svc.RefreshUsed(context.Background(), 5*time.Minute)
			if got := f.callCount(); got != tt.wantCalls {
				t.Fatalf("fetch calls = %d, want %d", got, tt.wantCalls)
			}
			if tt.wantCalls > 0 && f.calls[0].AllowSpawn {
				t.Error("RefreshUsed fetch had AllowSpawn true, want false")
			}

			// The used set is cleared after RefreshUsed, so a second call without
			// MarkUsed fetches nothing.
			svc.RefreshUsed(context.Background(), 5*time.Minute)
			if got := f.callCount(); got != tt.wantCalls {
				t.Errorf("fetch calls after second RefreshUsed = %d, want %d", got, tt.wantCalls)
			}
		})
	}
}

func TestRefreshUsedResolvesAliasMarkedBeforeRegister(t *testing.T) {
	svc, _ := newTestService(t, nil)
	svc.MarkUsed("late")
	svc.RefreshUsed(context.Background(), 0)

	f := &fakeFetcher{snap: sampleSnapshot("k", testBase, 50)}
	svc.Register(Account{Key: "k", Aliases: []string{"late"}, Fetcher: f})
	svc.RefreshUsed(context.Background(), 0)

	if got := f.callCount(); got != 1 {
		t.Errorf("fetch calls after registering marked alias = %d, want 1", got)
	}
}

func TestRefreshSkipsAccountAlreadyInFlight(t *testing.T) {
	svc, _ := newTestService(t, nil)
	f := &fakeFetcher{
		snap:    sampleSnapshot("k", testBase, 50),
		started: make(chan struct{}, 1),
		block:   make(chan struct{}),
	}
	svc.Register(Account{Key: "k", Aliases: []string{"a"}, Fetcher: f})

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		svc.RefreshAll(context.Background())
	}()
	select {
	case <-f.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first fetch did not start")
	}
	if v := svc.Views("")[0]; !v.Refreshing {
		t.Error("view not marked Refreshing while its fetch is in flight")
	}

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		svc.RefreshAll(context.Background())
	}()
	select {
	case <-secondDone:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent refresh waited for the in-flight fetch instead of skipping it")
	}

	close(f.block)
	<-firstDone
	if got := f.callCount(); got != 1 {
		t.Errorf("fetch calls = %d, want 1", got)
	}
}

func TestRefreshErrorKeepsLatestAndTruncatesMessage(t *testing.T) {
	svc, _ := newTestService(t, nil)
	f := &fakeFetcher{err: errors.New(strings.Repeat("x", 200))}
	svc.Register(Account{Key: "k", Aliases: []string{"a"}, Fetcher: f})
	svc.Observe(sampleSnapshot("k", testBase, 10))
	<-svc.Updates()

	svc.RefreshAll(context.Background())

	v := svc.Views("")[0]
	if len(v.Err) > maxErrLen || v.Err == "" {
		t.Errorf("Err = %q (%d chars), want non-empty and at most %d", v.Err, len(v.Err), maxErrLen)
	}
	if v.Latest == nil || v.Latest.Source != SourcePassive || v.Latest.Windows[0].UsedPercent != 10 {
		t.Errorf("Latest after failed fetch = %+v, want the previous passive snapshot", v.Latest)
	}
	select {
	case <-svc.Updates():
	default:
		t.Error("failed fetch did not notify")
	}

	f.err = nil
	svc.RefreshAll(context.Background())
	if v := svc.Views("")[0]; v.Err != "" || v.Latest.Source != SourceFetch {
		t.Errorf("after successful fetch Err = %q, Source = %q; want cleared error and fetch source", v.Err, v.Latest.Source)
	}
}

func TestFetchTimeoutSelection(t *testing.T) {
	tests := []struct {
		name           string
		serviceTimeout time.Duration
		timed          bool
		fetcherTimeout time.Duration
		want           time.Duration
	}{
		{"default when fetcher is not a TimeoutFetcher", 0, false, 0, defaultFetchTimeout},
		{"service option when fetcher is not a TimeoutFetcher", 3 * time.Second, false, 0, 3 * time.Second},
		{"fetcher timeout overrides service option", 3 * time.Second, true, 20 * time.Second, 20 * time.Second},
		{"non-positive fetcher timeout uses service option", 3 * time.Second, true, 0, 3 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := &fakeFetcher{snap: sampleSnapshot("k", testBase, 1)}
			var f Fetcher = base
			if tt.timed {
				f = timedFetcher{fakeFetcher: base, timeout: tt.fetcherTimeout}
			}
			svc := NewService(ServiceOptions{
				Now:          func() time.Time { return testBase },
				Logf:         t.Logf,
				FetchTimeout: tt.serviceTimeout,
			})
			svc.Register(Account{Key: "k", Aliases: []string{"a"}, Fetcher: f})

			before := time.Now()
			svc.RefreshAll(context.Background())
			after := time.Now()

			if len(base.deadlines) != 1 {
				t.Fatalf("fetch saw %d deadlines, want 1", len(base.deadlines))
			}
			got := base.deadlines[0]
			if got.Before(before.Add(tt.want)) || got.After(after.Add(tt.want)) {
				t.Errorf("fetch deadline %s not within [%s, %s]", got, before.Add(tt.want), after.Add(tt.want))
			}
		})
	}
}

func TestViewsIncludeProjections(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "quota-history.json"))
	svc, clock := newTestService(t, store)
	svc.Register(Account{Key: "k", Aliases: []string{"k"}})
	svc.Observe(sampleSnapshot("k", testBase, 10))

	*clock = testBase.Add(30 * time.Minute)
	svc.Observe(sampleSnapshot("k", testBase.Add(30*time.Minute), 20))
	mustDrain(t, svc)

	v := svc.Views("")[0]
	if len(v.WindowProjections) != 1 || !v.WindowProjections[0].OK || !v.WindowProjections[0].BeforeReset {
		t.Errorf("WindowProjections = %+v, want one OK projection before reset", v.WindowProjections)
	}
	if len(v.BudgetProjections) != 1 || v.BudgetProjections[0].OK {
		t.Errorf("BudgetProjections = %+v, want one projection that is not OK (no budget rise)", v.BudgetProjections)
	}
}
