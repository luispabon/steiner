package quota

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestEnsureLoadedRetriesAfterReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quota-history.json")
	store := NewStore(path)
	// A directory at the store path makes every read fail.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	svc, _ := newTestService(t, store)

	// Both the first load and the retry inside Observe fail, so these
	// observations live only in memory.
	observed := sampleSnapshot("k1", testBase.Add(2*time.Hour), 40)
	svc.Observe(observed)
	svc.Observe(sampleSnapshot("k3", testBase.Add(-time.Hour), 5))

	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for _, snap := range []Snapshot{
		sampleSnapshot("k1", testBase, 10), // older than observed: must not overwrite
		sampleSnapshot("k2", testBase, 30), // absent in memory: must load
		sampleSnapshot("k3", testBase, 20), // newer than observed: must replace
	} {
		if err := store.Append(snap, testBase); err != nil {
			t.Fatalf("seed Append %s: %v", snap.AccountKey, err)
		}
	}

	svc.Register(Account{Key: "k1", Aliases: []string{"a"}})
	svc.Register(Account{Key: "k2", Aliases: []string{"b"}})
	svc.Register(Account{Key: "k3", Aliases: []string{"c"}})

	byKey := make(map[string]AccountView)
	for _, v := range svc.Views("") {
		byKey[v.Key] = v
	}
	want := map[string]struct {
		fetchedAt time.Time
		used      float64
	}{
		"k1": {observed.FetchedAt, 40},
		"k2": {testBase, 30},
		"k3": {testBase, 20},
	}
	for key, w := range want {
		v, ok := byKey[key]
		if !ok || v.Latest == nil {
			t.Errorf("%s has no Latest after retry", key)
			continue
		}
		if !v.Latest.FetchedAt.Equal(w.fetchedAt) || v.Latest.Windows[0].UsedPercent != w.used {
			t.Errorf("%s Latest = fetched %s at %v%%, want fetched %s at %v%%",
				key, v.Latest.FetchedAt, v.Latest.Windows[0].UsedPercent, w.fetchedAt, w.used)
		}
	}
}

func TestLoadReadsStoreOnceAndDoesNotNotify(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "quota-history.json"))
	if err := store.Append(sampleSnapshot("k1", testBase, 10), testBase); err != nil {
		t.Fatalf("seed Append: %v", err)
	}
	var logs int
	svc := NewService(ServiceOptions{Store: store, Logf: func(string, ...any) { logs++ }})
	svc.Load()
	if _, ok := svc.latest["k1"]; !ok {
		t.Error("Load did not populate the stored snapshot")
	}
	if len(svc.Updates()) != 0 {
		t.Error("Load notified Updates, want none")
	}

	// A corrupt file makes any further read fail and log. Staying silent proves
	// the second Load did not read the store again.
	if err := os.WriteFile(store.path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt store: %v", err)
	}
	svc.Load()
	if logs != 0 {
		t.Errorf("logged %d errors, want 0: second Load read the store again", logs)
	}
}

func TestLoadRetriesAfterReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quota-history.json")
	store := NewStore(path)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt fixture: %v", err)
	}
	svc := NewService(ServiceOptions{Store: store})
	svc.Load()
	if svc.loaded {
		t.Fatal("Load marked a failed read as loaded")
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := store.Append(sampleSnapshot("k1", testBase, 10), testBase); err != nil {
		t.Fatalf("seed Append: %v", err)
	}
	svc.Load()
	if _, ok := svc.latest["k1"]; !ok || !svc.loaded {
		t.Error("retried Load did not populate the stored snapshot")
	}
}

func TestViewsConcurrentFirstLoad(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "quota-history.json"))
	if err := store.Append(sampleSnapshot("k1", testBase, 10), testBase); err != nil {
		t.Fatalf("seed Append: %v", err)
	}
	svc, _ := newTestService(t, store)
	svc.Register(Account{Key: "k1", Aliases: []string{"a"}})

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			views := svc.Views("")
			if len(views) != 1 || views[0].Latest == nil {
				t.Errorf("Views = %d rows, want one k1 row with a stored snapshot", len(views))
			}
		})
	}
	wg.Wait()
}
