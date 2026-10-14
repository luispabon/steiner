package quota

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "quota-history.json"))
	a1 := sampleSnapshot("a", testBase, 10)
	a2 := sampleSnapshot("a", testBase.Add(time.Hour), 20)
	b1 := sampleSnapshot("b", testBase.Add(30*time.Minute), 5)
	for _, snap := range []Snapshot{a2, b1, a1} {
		if err := store.Append(snap, snap.FetchedAt); err != nil {
			t.Fatalf("Append(%s): %v", snap.FetchedAt, err)
		}
	}

	history, err := store.History("a")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("History(a) returned %d snapshots, want 2", len(history))
	}
	for i, want := range []Snapshot{a1, a2} {
		got := history[i]
		if !sameState(got, want) || !got.FetchedAt.Equal(want.FetchedAt) || got.Source != want.Source {
			t.Errorf("History(a)[%d] = %+v, want %+v", i, got, want)
		}
	}
	if empty, err := store.History("unknown"); err != nil || len(empty) != 0 {
		t.Errorf("History(unknown) = %d snapshots, err %v; want none", len(empty), err)
	}

	latest, err := store.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("Latest returned %d accounts, want 2", len(latest))
	}
	if !sameState(latest["a"], a2) {
		t.Errorf("Latest[a] = %+v, want %+v", latest["a"], a2)
	}
	if !sameState(latest["b"], b1) {
		t.Errorf("Latest[b] = %+v, want %+v", latest["b"], b1)
	}
}

func TestStoreAppendDedupe(t *testing.T) {
	loc := time.FixedZone("plus1", 3600)
	tests := []struct {
		name   string
		change func(*Snapshot)
		gap    time.Duration
		want   int
	}{
		{"identical within an hour is skipped", func(*Snapshot) {}, 30 * time.Minute, 1},
		{"identical at exactly one hour is stored", func(*Snapshot) {}, time.Hour, 2},
		{"identical after an hour is stored", func(*Snapshot) {}, 2 * time.Hour, 2},
		{"changed usage is stored", func(s *Snapshot) { s.Windows[0].UsedPercent++ }, 5 * time.Minute, 2},
		{"changed reset instant is stored", func(s *Snapshot) {
			s.Windows[0].ResetsAt = s.Windows[0].ResetsAt.Add(time.Second)
		}, 5 * time.Minute, 2},
		{"same reset instant in another zone is skipped", func(s *Snapshot) {
			s.Windows[0].ResetsAt = s.Windows[0].ResetsAt.In(loc)
		}, 5 * time.Minute, 1},
		{"changed budget limit is stored", func(s *Snapshot) { s.Budgets[0].Limit = ptr(60.0) }, 5 * time.Minute, 2},
		{"changed plan is stored", func(s *Snapshot) { s.Plan = "max" }, 5 * time.Minute, 2},
		{"changed source only is skipped", func(s *Snapshot) { s.Source = SourcePassive }, 5 * time.Minute, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(filepath.Join(t.TempDir(), "quota-history.json"))
			if err := store.Append(sampleSnapshot("a", testBase, 10), testBase); err != nil {
				t.Fatalf("first Append: %v", err)
			}
			next := sampleSnapshot("a", testBase.Add(tt.gap), 10)
			tt.change(&next)
			if err := store.Append(next, next.FetchedAt); err != nil {
				t.Fatalf("second Append: %v", err)
			}
			history, err := store.History("a")
			if err != nil {
				t.Fatalf("History: %v", err)
			}
			if len(history) != tt.want {
				t.Errorf("stored %d snapshots, want %d", len(history), tt.want)
			}
		})
	}
}

func TestStoreAppendPrunesSnapshotsOlderThanRetention(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "quota-history.json"))
	day := 24 * time.Hour
	old := sampleSnapshot("a", testBase.Add(-36*day), 10)
	kept := sampleSnapshot("b", testBase.Add(-34*day), 10)
	for _, snap := range []Snapshot{old, kept} {
		if err := store.Append(snap, snap.FetchedAt); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := store.Append(sampleSnapshot("c", testBase, 20), testBase); err != nil {
		t.Fatalf("Append at base: %v", err)
	}

	if got, _ := store.History("a"); len(got) != 0 {
		t.Errorf("36-day-old snapshot kept: %d entries", len(got))
	}
	if got, _ := store.History("b"); len(got) != 1 {
		t.Errorf("34-day-old snapshot pruned: %d entries, want 1", len(got))
	}
}

func TestStoreFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes are not enforced on Windows")
	}
	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, "quota-history.json")
	if err := NewStore(path).Append(sampleSnapshot("a", testBase, 10), testBase); err != nil {
		t.Fatalf("Append: %v", err)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat state dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("state dir mode = %o, want 700", perm)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat history file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf("history file mode = %o, want 600", perm)
	}
}

func TestStoreConcurrentAppendsThroughTwoStores(t *testing.T) {
	requireFileLocking(t)
	path := filepath.Join(t.TempDir(), "quota-history.json")
	const perStore = 20

	var wg sync.WaitGroup
	for g := range 2 {
		store := NewStore(path)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perStore {
				n := g*perStore + i
				// Distinct usage and fetch times keep every append from being deduplicated.
				snap := sampleSnapshot("a", testBase.Add(time.Duration(n)*time.Second), float64(n))
				if err := store.Append(snap, snap.FetchedAt); err != nil {
					t.Errorf("Append %d: %v", n, err)
				}
			}
		}()
	}
	wg.Wait()

	history, err := NewStore(path).History("a")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 2*perStore {
		t.Errorf("stored %d snapshots, want %d: concurrent appends clobbered each other", len(history), 2*perStore)
	}
}

func TestStoreMissingFileIsEmpty(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "absent.json"))
	if history, err := store.History("a"); err != nil || len(history) != 0 {
		t.Errorf("History = %d snapshots, err %v; want empty without error", len(history), err)
	}
	if latest, err := store.Latest(); err != nil || len(latest) != 0 {
		t.Errorf("Latest = %d accounts, err %v; want empty without error", len(latest), err)
	}
}

func TestStoreCorruptFileIsPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quota-history.json")
	corrupt := []byte("{not json")
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatalf("write corrupt fixture: %v", err)
	}
	store := NewStore(path)
	if _, err := store.History("a"); err == nil {
		t.Error("History on corrupt file: want error")
	}
	if _, err := store.Latest(); err == nil {
		t.Error("Latest on corrupt file: want error")
	}
	if err := store.Append(sampleSnapshot("a", testBase, 10), testBase); err == nil {
		t.Error("Append on corrupt file: want error")
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, corrupt) {
		t.Errorf("corrupt file was rewritten to %q", got)
	}
}

func TestStoreRejectsUnsupportedVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quota-history.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"snapshots":[]}`), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := NewStore(path).History("a"); err == nil {
		t.Error("History with version 2: want error")
	}
}

func TestDefaultStorePath(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		name string
		xdg  string
		want string
	}{
		{"uses XDG_STATE_HOME", "/xdg/state", filepath.Join("/xdg/state", "steiner", "quota-history.json")},
		{"falls back to home state dir", "", filepath.Join(home, ".local", "state", "steiner", "quota-history.json")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", tt.xdg)
			t.Setenv("HOME", home)
			got, err := DefaultStorePath()
			if err != nil {
				t.Fatalf("DefaultStorePath: %v", err)
			}
			if got != tt.want {
				t.Errorf("DefaultStorePath() = %q, want %q", got, tt.want)
			}
		})
	}
}
