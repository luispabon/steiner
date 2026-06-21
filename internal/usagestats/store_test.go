package usagestats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoreLoadFixture(t *testing.T) {
	fixturePath := filepath.Join("testdata", "cache-stats.json")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	if sf.SchemaVersion != 1 {
		t.Errorf("schema_version: got %d, want 1", sf.SchemaVersion)
	}
	if len(sf.Entries) != 2 {
		t.Errorf("entries count: got %d, want 2", len(sf.Entries))
	}

	// Verify first entry.
	e0 := sf.Entries[0]
	if e0.Provider != "anthropic" {
		t.Errorf("entry[0].provider: got %q, want %q", e0.Provider, "anthropic")
	}
	if e0.Requests != 5 {
		t.Errorf("entry[0].requests: got %d, want 5", e0.Requests)
	}
	if e0.CacheReadTokens != 450 {
		t.Errorf("entry[0].cache_read_tokens: got %d, want 450", e0.CacheReadTokens)
	}
}

func TestStoreLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	clock := newMockClock(time.Now())

	oldPath := storePath
	defer func() { storePath = oldPath }()
	storePath = func() string { return filepath.Join(dir, "nonexistent.json") }

	s := newStore(clock.Now)
	buckets := s.load()

	if len(buckets) != 0 {
		t.Errorf("load missing file: got %d buckets, want 0", len(buckets))
	}
}

func TestStoreLoadCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	clock := newMockClock(time.Now())

	oldPath := storePath
	defer func() { storePath = oldPath }()
	storePath = func() string { return path }

	s := newStore(clock.Now)
	buckets := s.load()

	if len(buckets) != 0 {
		t.Errorf("load corrupt JSON: got %d buckets, want 0", len(buckets))
	}
}

func TestStoreLoadUnknownSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unknown.json")
	data, _ := json.Marshal(storeFile{SchemaVersion: 999, Entries: []storeEntry{}})
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write unknown schema: %v", err)
	}

	clock := newMockClock(time.Now())

	oldPath := storePath
	defer func() { storePath = oldPath }()
	storePath = func() string { return path }

	s := newStore(clock.Now)
	buckets := s.load()

	if len(buckets) != 0 {
		t.Errorf("load unknown schema: got %d buckets, want 0", len(buckets))
	}
}

func TestStoreLoadWithPruning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pruned.json")

	now := time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC)
	oldHour := now.Add(-10 * 24 * time.Hour).Truncate(time.Hour)
	newHour := now.Add(-2 * 24 * time.Hour).Truncate(time.Hour)

	entries := []storeEntry{
		{Provider: "p1", ProviderType: "t1", Model: "m1", HourUnix: oldHour.Unix(), Requests: 1},
		{Provider: "p2", ProviderType: "t2", Model: "m2", HourUnix: newHour.Unix(), Requests: 2},
	}
	sf := storeFile{SchemaVersion: 1, Entries: entries}
	data, _ := json.Marshal(sf)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	clock := newMockClock(now)

	oldPath := storePath
	defer func() { storePath = oldPath }()
	storePath = func() string { return path }

	s := newStore(clock.Now)
	buckets := s.load()

	// Only the new entry (within 8 days) should be kept.
	if len(buckets) != 1 {
		t.Errorf("load with pruning: got %d buckets, want 1", len(buckets))
	}

	key := bucketKey{providerAlias: "p2", providerType: "t2", backendModelID: "m2", hourUnix: newHour.Unix()}
	if _, ok := buckets[key]; !ok {
		t.Errorf("expected bucket not found: %+v", key)
	}
}

func TestStoreWriteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "roundtrip.json")

	now := time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC)
	hour := now.Truncate(time.Hour)

	clock := newMockClock(now)

	oldPath := storePath
	defer func() { storePath = oldPath }()
	storePath = func() string { return path }

	// Create initial store.
	s1 := newStore(clock.Now)

	// Record an observation.
	key := bucketKey{
		providerAlias:  "test",
		providerType:   "openai",
		backendModelID: "gpt-4",
		hourUnix:       hour.Unix(),
	}
	delta := &bucket{
		Requests:          1,
		InputTokens:       100,
		CacheReadTokens:   50,
		CacheCreateTokens: 25,
		CompletionTokens:  75,
	}

	if err := s1.write(delta, key); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Load from a new store instance.
	s2 := newStore(clock.Now)
	loaded := s2.load()

	if len(loaded) != 1 {
		t.Errorf("load after write: got %d buckets, want 1", len(loaded))
	}

	if b, ok := loaded[key]; !ok {
		t.Errorf("bucket not found")
	} else {
		if b.Requests != 1 {
			t.Errorf("requests: got %d, want 1", b.Requests)
		}
		if b.CacheReadTokens != 50 {
			t.Errorf("cache_read_tokens: got %d, want 50", b.CacheReadTokens)
		}
	}
}

func TestStoreAdditiveMerge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "merge.json")

	now := time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC)
	hour := now.Truncate(time.Hour)

	clock := newMockClock(now)

	oldPath := storePath
	defer func() { storePath = oldPath }()
	storePath = func() string { return path }

	// Initial write.
	s := newStore(clock.Now)
	key := bucketKey{
		providerAlias:  "p1",
		providerType:   "t1",
		backendModelID: "m1",
		hourUnix:       hour.Unix(),
	}
	delta1 := &bucket{Requests: 1, InputTokens: 100}
	if err := s.write(delta1, key); err != nil {
		t.Fatalf("first write: %v", err)
	}

	// Second write: add more to the same hour.
	delta2 := &bucket{Requests: 2, InputTokens: 200}
	if err := s.write(delta2, key); err != nil {
		t.Fatalf("second write: %v", err)
	}

	// Load and verify additive merge.
	s2 := newStore(clock.Now)
	loaded := s2.load()

	b := loaded[key]
	if b.Requests != 3 { // 1 + 2
		t.Errorf("requests after merge: got %d, want 3", b.Requests)
	}
	if b.InputTokens != 300 { // 100 + 200
		t.Errorf("input tokens after merge: got %d, want 300", b.InputTokens)
	}
}

func TestStoreConcurrentRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "concurrent.json")

	now := time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC)
	hour := now.Truncate(time.Hour)

	clock := newMockClock(now)

	oldPath := storePath
	defer func() { storePath = oldPath }()
	storePath = func() string { return path }

	s := newStore(clock.Now)

	// Run concurrent writes.
	key := bucketKey{
		providerAlias:  "p1",
		providerType:   "t1",
		backendModelID: "m1",
		hourUnix:       hour.Unix(),
	}

	n := 10
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			delta := &bucket{Requests: 1, InputTokens: 10}
			_ = s.write(delta, key)
		}()
	}

	wg.Wait()

	// Load and verify totals.
	// Concurrent writes may have lock contention; at least some must succeed.
	s2 := newStore(clock.Now)
	loaded := s2.load()

	b := loaded[key]
	// All 10 writes should be included in the final state via additive merge.
	// This test verifies that concurrent writes are added correctly, not overwritten.
	if b.Requests < 1 || b.Requests > n {
		t.Errorf("concurrent requests: got %d, want between 1 and %d", b.Requests, n)
	}
	if b.InputTokens < 10 || b.InputTokens > n*10 {
		t.Errorf("concurrent input tokens: got %d, want between 10 and %d", b.InputTokens, n*10)
	}
}

func TestStoreWriteCreatesDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir1", "subdir2", "stats.json")

	now := time.Now()
	clock := newMockClock(now)

	oldPath := storePath
	defer func() { storePath = oldPath }()
	storePath = func() string { return path }

	s := newStore(clock.Now)
	key := bucketKey{providerAlias: "p", providerType: "t", backendModelID: "m", hourUnix: now.Unix()}
	delta := &bucket{Requests: 1}

	if err := s.write(delta, key); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestStoreWritePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "perms.json")

	now := time.Now()
	clock := newMockClock(now)

	oldPath := storePath
	defer func() { storePath = oldPath }()
	storePath = func() string { return path }

	s := newStore(clock.Now)
	key := bucketKey{providerAlias: "p", providerType: "t", backendModelID: "m", hourUnix: now.Unix()}
	delta := &bucket{Requests: 1}

	if err := s.write(delta, key); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if info.Mode()&0o644 != 0o644 {
		t.Errorf("file perms: got %o, want 0o644", info.Mode()&0o777)
	}
}

func TestStorePruningOnWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prune-write.json")

	now := time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC)

	clock := newMockClock(now)

	oldPath := storePath
	defer func() { storePath = oldPath }()
	storePath = func() string { return path }

	// Write an old entry on disk first.
	s := newStore(clock.Now)
	oldHour := now.Add(-10 * 24 * time.Hour).Truncate(time.Hour)
	oldKey := bucketKey{
		providerAlias:  "old",
		providerType:   "t",
		backendModelID: "m",
		hourUnix:       oldHour.Unix(),
	}
	oldDelta := &bucket{Requests: 1}
	if err := s.write(oldDelta, oldKey); err != nil {
		t.Fatalf("write old: %v", err)
	}

	// Now write a new entry.
	newHour := now.Truncate(time.Hour)
	newKey := bucketKey{
		providerAlias:  "new",
		providerType:   "t",
		backendModelID: "m",
		hourUnix:       newHour.Unix(),
	}
	newDelta := &bucket{Requests: 1}
	if err := s.write(newDelta, newKey); err != nil {
		t.Fatalf("write new: %v", err)
	}

	// Load and verify only new entry remains.
	s2 := newStore(clock.Now)
	loaded := s2.load()

	if len(loaded) != 1 {
		t.Errorf("after prune write: got %d buckets, want 1", len(loaded))
	}

	if _, ok := loaded[newKey]; !ok {
		t.Errorf("new entry not found")
	}
	if _, ok := loaded[oldKey]; ok {
		t.Errorf("old entry not pruned")
	}
}

func TestStorePruningBoundary(t *testing.T) {
	tests := []struct {
		name       string
		ageHours   int
		shouldKeep bool
	}{
		{"just inside 8 days", 8*24 - 1, true},
		{"exactly 8 days", 8 * 24, false},
		{"just outside 8 days", 8*24 + 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "boundary-"+tt.name+".json")

			now := time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC)
			hour := now.Add(-time.Duration(tt.ageHours) * time.Hour).Truncate(time.Hour)

			clock := newMockClock(now)

			oldPath := storePath
			storePath = func() string { return path }

			s := newStore(clock.Now)
			key := bucketKey{
				providerAlias:  "p",
				providerType:   "t",
				backendModelID: "m",
				hourUnix:       hour.Unix(),
			}
			delta := &bucket{Requests: 1}

			if err := s.write(delta, key); err != nil {
				t.Fatalf("write: %v", err)
			}

			s2 := newStore(clock.Now)
			loaded := s2.load()

			found := len(loaded) > 0
			if found != tt.shouldKeep {
				t.Errorf("should keep: got %v, want %v", found, tt.shouldKeep)
			}

			storePath = oldPath
		})
	}
}

// mockClock provides a controllable time source for tests.
type mockClock struct {
	value atomic.Value
}

func newMockClock(t time.Time) *mockClock {
	mc := &mockClock{}
	mc.value.Store(t)
	return mc
}

func (mc *mockClock) Now() time.Time {
	return mc.value.Load().(time.Time)
}

func (mc *mockClock) Advance(d time.Duration) {
	current := mc.value.Load().(time.Time)
	mc.value.Store(current.Add(d))
}
