package usagestats

import (
	"path/filepath"
	"testing"
	"time"
)

// TestStoreRetentionSizedRoundTrip writes a store at full dashboard shape:
// every retained hour for three models across three call surfaces.
func TestStoreRetentionSizedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bulk.json")

	now := time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC)
	clock := newMockClock(now)

	oldPath := storePath
	defer func() { storePath = oldPath }()
	storePath = func() string { return path }

	models := []string{"m1", "m2", "m3"}
	sources := []Source{SourceParent, SourceSubAgent, SourceAdvisor}
	hour := now.Add(-time.Duration(retentionDays*24-1) * time.Hour)

	want := make(map[bucketKey]*bucket, retentionDays*24*len(models)*len(sources))
	for d := 0; d < retentionDays; d++ {
		for h := 0; h < 24; h++ {
			for _, model := range models {
				for _, source := range sources {
					want[bucketKey{
						providerAlias:  "main",
						providerType:   "anthropic",
						backendModelID: model,
						hourUnix:       hour.Unix(),
						source:         source,
					}] = &bucket{Requests: 1, InputTokens: 10, CacheReadTokens: 20, CacheCreateTokens: 30, CompletionTokens: 40}
					hour = hour.Add(time.Hour)
				}
			}
		}
	}

	s := newStore(clock.Now)
	if err := s.writeBatch(want); err != nil {
		t.Fatalf("write %d buckets: %v", len(want), err)
	}

	loaded := newStore(clock.Now).load()
	if len(loaded) != len(want) {
		t.Fatalf("loaded buckets = %d, want %d", len(loaded), len(want))
	}
	for key, expected := range want {
		got, ok := loaded[key]
		if !ok {
			t.Fatalf("bucket missing after round trip: %+v", key)
		}
		if *got != *expected {
			t.Errorf("bucket %+v = %+v, want %+v", key, *got, *expected)
		}
	}
}
