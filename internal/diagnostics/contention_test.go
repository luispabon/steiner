package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/filelock"
)

// holdStreamLock takes the stream's cross-process lock and skips the test when
// the platform cannot provide one. Release it to hand the stream back.
func holdStreamLock(t *testing.T, path string) *filelock.Lock {
	t.Helper()
	lock, err := filelock.Acquire(context.Background(), path+lockSuffix)
	if err != nil {
		t.Fatalf("acquire stream lock: %v", err)
	}
	if lock.Unsupported() {
		_ = lock.Release()
		t.Skip("file locking unavailable on this platform")
	}
	return lock
}

// requireStreamLock skips a test that depends on cross-process exclusion when
// the platform cannot provide it.
func requireStreamLock(t *testing.T, path string) {
	t.Helper()
	lock := holdStreamLock(t, path)
	if err := lock.Release(); err != nil {
		t.Fatalf("release stream lock: %v", err)
	}
}

func TestHeldLockStartupAndAppendsStayBestEffort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.jsonl")
	seedStream(t, path, 40*24*time.Hour)

	lock := holdStreamLock(t, path)

	started := time.Now()
	w, err := New(Options{Dir: dir, Streams: Streams{Cache: true}, RetentionDays: 30})
	startup := time.Since(started)
	if err != nil {
		t.Fatalf("New() error = %v while the stream lock was held, want best-effort startup", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if startup > 2*time.Second {
		t.Errorf("New() took %v while the stream lock was held, want a bounded wait", startup)
	}

	// Retention cannot run without the lock, so the expired record stays.
	if records := readRecords(t, path); len(records) != 1 {
		t.Fatalf("records = %+v, want the unpruned seed record", records)
	}

	// A contended append is dropped rather than written without exclusion.
	w.Write(Record{Kind: KindCache, AgentID: "contended"})
	if records := readRecords(t, path); len(records) != 1 {
		t.Fatalf("records = %+v, want the contended record dropped", records)
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("release stream lock: %v", err)
	}
	time.Sleep(lockBackoff + 50*time.Millisecond)

	w.Write(Record{Kind: KindCache, AgentID: "recovered"})
	records := readRecords(t, path)
	if len(records) != 2 || records[1].AgentID != "recovered" {
		t.Fatalf("records = %+v, want the record written after the lock was released", records)
	}
}

func TestContendedAppendsStayBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	w, err := New(Options{Dir: dir, Streams: Streams{Tool: true}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	lock := holdStreamLock(t, path)

	const writers, perWriter = 4, 25
	started := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				w.Write(Record{Kind: KindTool, AgentID: fmt.Sprintf("writer-%d-%d", i, j)})
			}
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(started)

	if limit := 2 * time.Second; elapsed > limit {
		t.Errorf("%d contended writes took %v, want the lock wait charged once per backoff window", writers*perWriter, elapsed)
	}
	if records := readRecords(t, path); len(records) != 0 {
		t.Fatalf("records = %d, want every contended record dropped", len(records))
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("release stream lock: %v", err)
	}
	time.Sleep(lockBackoff + 50*time.Millisecond)

	w.Write(Record{Kind: KindTool, AgentID: "released"})
	records := readRecords(t, path)
	if len(records) != 1 || records[0].AgentID != "released" {
		t.Fatalf("records = %+v, want the record written after the lock was released", records)
	}
}
