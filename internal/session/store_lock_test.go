package session

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/filelock"
)

// saveTaggedSessions saves count sessions whose IDs are prefix-N and whose
// UpdatedAt increases by one second per session, starting at base.
func saveTaggedSessions(t *testing.T, store *Store, prefix string, count int, base time.Time) {
	t.Helper()
	for i := range count {
		id := fmt.Sprintf("%s-%d", prefix, i)
		session := Session{ID: id, CreatedAt: base, UpdatedAt: base.Add(time.Duration(i) * time.Second)}
		if err := store.Save(session); err != nil {
			t.Fatalf("Save(%s) failed: %v", id, err)
		}
	}
}

// indexIDs returns the index entry IDs in slice order.
func indexIDs(entries []IndexEntry) []string {
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return ids
}

func TestSeparateStoresRetainBothEntries(t *testing.T) {
	dir := t.TempDir()
	a, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore(a) failed: %v", err)
	}
	b, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore(b) failed: %v", err)
	}

	base := time.Now().UTC()
	saveTaggedSessions(t, a, "a", 2, base)
	saveTaggedSessions(t, b, "b", 2, base.Add(time.Minute))

	want := []string{"b-1", "b-0", "a-1", "a-0"}
	for name, store := range map[string]*Store{"a": a, "b": b} {
		entries, err := store.List()
		if err != nil {
			t.Fatalf("List(%s) failed: %v", name, err)
		}
		if got := indexIDs(entries); !slices.Equal(got, want) {
			t.Errorf("List(%s) IDs = %v, want %v", name, got, want)
		}
	}
	checkIndexIDs(t, dir, a, want)
}

func TestListRefreshesIndexFromDisk(t *testing.T) {
	dir := t.TempDir()
	a, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore(a) failed: %v", err)
	}
	b, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore(b) failed: %v", err)
	}

	base := time.Now().UTC()
	saveTaggedSessions(t, a, "a", 1, base)
	entries, err := b.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if got := indexIDs(entries); !slices.Equal(got, []string{"a-0"}) {
		t.Errorf("List IDs = %v, want [a-0]", got)
	}

	if err := a.Delete("a-0"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	entries, err = b.List()
	if err != nil {
		t.Fatalf("List after delete failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List after delete returned %v, want no entries", indexIDs(entries))
	}

	// A missing index file resets the cached entries instead of replaying them.
	saveTaggedSessions(t, a, "a", 1, base)
	if err := os.Remove(filepath.Join(dir, indexFile)); err != nil {
		t.Fatalf("remove index: %v", err)
	}
	entries, err = b.List()
	if err != nil {
		t.Fatalf("List with missing index failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List with missing index returned %v, want no entries", indexIDs(entries))
	}
}

func TestDeletePreservesEntriesSavedByOtherStore(t *testing.T) {
	dir := t.TempDir()
	a, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore(a) failed: %v", err)
	}
	b, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore(b) failed: %v", err)
	}

	base := time.Now().UTC()
	saveTaggedSessions(t, a, "a", 1, base)
	saveTaggedSessions(t, b, "b", 1, base.Add(time.Minute))

	if err := b.Delete("a-0"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	checkIndexIDs(t, dir, a, []string{"b-0"})
}

func TestEvictionAccountsForEntriesFromOtherStores(t *testing.T) {
	dir := t.TempDir()
	a, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore(a) failed: %v", err)
	}
	b, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore(b) failed: %v", err)
	}

	base := time.Now().UTC().Add(-time.Hour)
	saveTaggedSessions(t, a, "a", maxSessions-1, base)
	// The two newest sessions come from the other store, so only a reload under
	// the index lock makes b's save see the overflow and evict the oldest.
	saveTaggedSessions(t, b, "b", 2, base.Add(maxSessions*time.Minute))

	entries, err := a.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != maxSessions {
		t.Fatalf("List returned %d entries, want %d", len(entries), maxSessions)
	}
	if _, err := os.Stat(filepath.Join(dir, "a-0"+sessionExt)); !os.IsNotExist(err) {
		t.Errorf("oldest session file survived eviction: %v", err)
	}
	checkIndexIDs(t, dir, b, indexIDs(entries))
}

func TestConcurrentSavesAcrossStoresKeepAllEntries(t *testing.T) {
	dir := t.TempDir()
	const stores = 4
	const perStore = 4
	base := time.Now().UTC()

	var wg sync.WaitGroup
	errCh := make(chan error, stores)
	for s := range stores {
		store, err := NewStore(dir)
		if err != nil {
			t.Fatalf("NewStore(%d) failed: %v", s, err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perStore {
				id := fmt.Sprintf("s%d-%d", s, i)
				session := Session{ID: id, CreatedAt: base, UpdatedAt: base.Add(time.Duration(i) * time.Second)}
				if err := store.Save(session); err != nil {
					errCh <- fmt.Errorf("Save(%s): %w", id, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	fresh, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore(fresh) failed: %v", err)
	}
	entries, err := fresh.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	want := stores * perStore
	if len(entries) != want {
		t.Fatalf("List returned %d entries, want %d", len(entries), want)
	}
	seen := make(map[string]bool, want)
	for _, e := range entries {
		seen[e.ID] = true
	}
	for s := range stores {
		for i := range perStore {
			id := fmt.Sprintf("s%d-%d", s, i)
			if !seen[id] {
				t.Errorf("entry %q lost", id)
			}
		}
	}
}

func TestSaveFailsWhenIndexLockCannotBeOpened(t *testing.T) {
	dir := t.TempDir()
	// Skip platforms without flock: Acquire never opens the lock file there.
	probe, err := filelock.Acquire(context.Background(), filepath.Join(dir, "probe.lock"))
	if err != nil {
		t.Fatalf("probe Acquire failed: %v", err)
	}
	if probe.Unsupported() {
		_ = probe.Release()
		t.Skip("platform cannot lock files")
	}
	if err := probe.Release(); err != nil {
		t.Fatalf("probe Release failed: %v", err)
	}

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, indexLockFile)); err != nil {
		t.Fatalf("remove lock file: %v", err)
	}
	// A directory at the lock path makes the lock open fail with an ordinary
	// error instead of degrading to an unsupported lock.
	if err := os.Mkdir(filepath.Join(dir, indexLockFile), 0o700); err != nil {
		t.Fatalf("create lock directory: %v", err)
	}

	if err := store.Save(Session{ID: "s", UpdatedAt: time.Now()}); err == nil || !strings.Contains(err.Error(), "acquire index lock") {
		t.Errorf("Save error = %v, want index lock failure", err)
	}
	if _, err := store.List(); err == nil || !strings.Contains(err.Error(), "acquire index lock") {
		t.Errorf("List error = %v, want index lock failure", err)
	}
	if err := store.Delete("s"); err == nil || !strings.Contains(err.Error(), "acquire index lock") {
		t.Errorf("Delete error = %v, want index lock failure", err)
	}
	if _, err := NewStore(dir); err == nil || !strings.Contains(err.Error(), "acquire index lock") {
		t.Errorf("NewStore error = %v, want index lock failure", err)
	}
}

func TestCorruptIndexStillFails(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	saveTaggedSessions(t, store, "s", 1, time.Now().UTC())
	if err := os.WriteFile(filepath.Join(dir, indexFile), []byte("{"), 0o600); err != nil {
		t.Fatalf("corrupt index: %v", err)
	}

	if _, err := NewStore(dir); err == nil || !strings.Contains(err.Error(), "load index") {
		t.Errorf("NewStore error = %v, want load index failure", err)
	}
	if err := store.Save(Session{ID: "s-1", UpdatedAt: time.Now()}); err == nil || !strings.Contains(err.Error(), "load index") {
		t.Errorf("Save error = %v, want load index failure", err)
	}
	if _, err := store.List(); err == nil || !strings.Contains(err.Error(), "load index") {
		t.Errorf("List error = %v, want load index failure", err)
	}
	if err := store.Delete("s-0"); err == nil || !strings.Contains(err.Error(), "load index") {
		t.Errorf("Delete error = %v, want load index failure", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "s-0"+sessionExt)); err != nil {
		t.Errorf("session file removed by failed Delete: %v", err)
	}
}

const (
	storeHelperDirEnv   = "STEINER_SESSION_STORE_HELPER_DIR"
	storeHelperCountEnv = "STEINER_SESSION_STORE_HELPER_COUNT"
)

// TestSessionStoreHelperProcess is the child half of
// TestCrossProcessSavesRetainBothEntries. It saves the requested number of
// sessions into the shared store directory and exits.
func TestSessionStoreHelperProcess(t *testing.T) {
	dir := os.Getenv(storeHelperDirEnv)
	if dir == "" {
		t.Skip("helper process only")
	}
	count, err := strconv.Atoi(os.Getenv(storeHelperCountEnv))
	if err != nil {
		t.Fatalf("parse %s: %v", storeHelperCountEnv, err)
	}
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	base := time.Now().UTC()
	for i := range count {
		id := fmt.Sprintf("child-%d", i)
		session := Session{ID: id, CreatedAt: base, UpdatedAt: base.Add(time.Duration(i) * time.Second)}
		if err := store.Save(session); err != nil {
			t.Fatalf("Save(%s) failed: %v", id, err)
		}
	}
}

func TestCrossProcessSavesRetainBothEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a subprocess")
	}
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	const count = 5

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestSessionStoreHelperProcess$")
	cmd.Env = append(os.Environ(), storeHelperDirEnv+"="+dir, storeHelperCountEnv+"="+strconv.Itoa(count))
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	base := time.Now().UTC()
	for i := range count {
		id := fmt.Sprintf("parent-%d", i)
		session := Session{ID: id, CreatedAt: base, UpdatedAt: base.Add(time.Duration(i) * time.Second)}
		if err := store.Save(session); err != nil {
			t.Fatalf("Save(%s) failed: %v\nhelper output:\n%s", id, err, output.String())
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper process failed: %v\n%s", err, output.String())
	}

	entries, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 2*count {
		t.Fatalf("List returned %d entries, want %d\nhelper output:\n%s", len(entries), 2*count, output.String())
	}
	seen := make(map[string]bool, 2*count)
	for _, e := range entries {
		seen[e.ID] = true
	}
	for i := range count {
		for _, prefix := range []string{"parent", "child"} {
			id := fmt.Sprintf("%s-%d", prefix, i)
			if !seen[id] {
				t.Errorf("entry %q lost", id)
			}
		}
	}
}
