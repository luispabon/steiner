//go:build unix

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/usagestats"
)

// TestCloseRuntimeDrainsUsageStatsWriter pins that runtime teardown waits for
// queued usage deltas: the store lock is held, so closeRuntime cannot return
// until the lock is released and the writer has persisted them.
func TestCloseRuntimeDrainsUsageStatsWriter(t *testing.T) {
	_, rec, release := holdUsageStatsStoreLock(t)

	rec.Record(usagestats.Observation{
		ProviderAlias:    "p",
		ProviderType:     "openai",
		BackendModelID:   "m",
		PromptTokens:     10,
		CompletionTokens: 2,
		Source:           usagestats.SourceParent,
	})

	rt := cliRuntime{usageRecorder: rec, events: output.NoopSink{}}
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		closeRuntime(&rt)
	}()

	select {
	case <-closed:
		t.Fatal("closeRuntime returned while a usage stats write was still blocked on the store lock")
	case <-time.After(100 * time.Millisecond):
	}

	release()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("closeRuntime did not finish after the store lock was released")
	}

	read := usagestats.New(nil)
	t.Cleanup(read.Close)
	rows := read.Window(time.Hour).Rows
	if len(rows) != 1 {
		t.Fatalf("persisted rows = %d, want 1", len(rows))
	}
	if rows[0].Requests != 1 || rows[0].InputTokens != 10 {
		t.Errorf("persisted row = %+v, want 1 request and 10 input tokens", rows[0])
	}
}

// TestCloseRuntimeWarnsWhenUsageStatsDrainExpires pins the deadline path: when
// the store lock outlives usageStatsDrainTimeout, teardown reports a
// session_health warning naming the flush instead of failing silently.
func TestCloseRuntimeWarnsWhenUsageStatsDrainExpires(t *testing.T) {
	path, rec, release := holdUsageStatsStoreLock(t)

	oldTimeout := usageStatsDrainTimeout
	// Long enough for the writer goroutine to claim the queued delta before the
	// deadline expires; the post-release assertion depends on that claim.
	usageStatsDrainTimeout = 250 * time.Millisecond
	t.Cleanup(func() { usageStatsDrainTimeout = oldTimeout })

	rec.Record(usagestats.Observation{
		ProviderAlias:  "p",
		ProviderType:   "openai",
		BackendModelID: "m",
		PromptTokens:   10,
		Source:         usagestats.SourceParent,
	})

	var mu sync.Mutex
	var events []output.Event
	sink := output.SinkFunc(func(event output.Event) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})

	rt := cliRuntime{usageRecorder: rec, events: sink}
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		closeRuntime(&rt)
	}()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("closeRuntime did not return after the drain deadline expired")
	}

	mu.Lock()
	emitted := append([]output.Event(nil), events...)
	mu.Unlock()
	warned := false
	for _, event := range emitted {
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		if strings.Contains(string(data), "flush usage stats") {
			warned = true
			break
		}
	}
	if !warned {
		t.Fatalf("no usage stats flush warning emitted, events: %v", emitted)
	}

	// The recorder is closed already. The delta was claimed before the deadline
	// and is blocked on the lock, so it must land once the lock is released; wait
	// for the store file both to prove that and to keep the write clear of this
	// test's temp state dir cleanup.
	release()
	appeared := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			appeared = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !appeared {
		t.Fatal("the claimed usage stats write never reached the store after the lock was released")
	}
}

// holdUsageStatsStoreLock isolates the cache-stats store to the test's temp state
// home and holds its flock, returning the store path, a recorder writing to that
// path, and a release func that unlocks it. Both the lock and the recorder are
// released on cleanup, lock first so a drain never waits on a lock the test
// still holds.
func holdUsageStatsStoreLock(t *testing.T) (string, *usagestats.Recorder, func()) {
	t.Helper()
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv(usagestats.TelemetryEnvVar, "")

	rec := usagestats.New(nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := rec.Drain(ctx); err != nil {
			t.Errorf("drain usage stats recorder: %v", err)
		}
		rec.Close()
	})

	path := filepath.Join(stateDir, "steiner", "cache-stats.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create state dir: %v", err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("open store lock: %v", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatalf("lock store: %v", err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
				t.Errorf("unlock store: %v", err)
			}
			if err := lock.Close(); err != nil {
				t.Errorf("close store lock: %v", err)
			}
		})
	}
	t.Cleanup(release)
	return path, rec, release
}
