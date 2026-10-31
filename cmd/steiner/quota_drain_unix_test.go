//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/quota"
)

// TestCloseRuntimeDrainsQuotaWriter pins that runtime teardown waits for queued
// quota snapshots: the store lock is held, so closeRuntime cannot return until
// the lock is released and the writer has appended.
func TestCloseRuntimeDrainsQuotaWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quota-history.json")
	lock, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("open store lock: %v", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatalf("lock store: %v", err)
	}
	held := true
	release := func() {
		if !held {
			return
		}
		held = false
		if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
			t.Errorf("unlock store: %v", err)
		}
		if err := lock.Close(); err != nil {
			t.Errorf("close store lock: %v", err)
		}
	}
	t.Cleanup(release)

	svc := quota.NewService(quota.ServiceOptions{Store: quota.NewStore(path)})
	svc.Observe(quota.Snapshot{AccountKey: "k", FetchedAt: time.Now()})

	rt := cliRuntime{quotaService: svc, events: output.NoopSink{}}
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		closeRuntime(&rt)
	}()

	select {
	case <-closed:
		t.Fatal("closeRuntime returned while a quota append was still blocked on the store lock")
	case <-time.After(100 * time.Millisecond):
	}

	release()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("closeRuntime did not finish after the store lock was released")
	}

	drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.Drain(drainCtx); err != nil {
		t.Fatalf("Drain after teardown: %v", err)
	}
	latest, err := quota.NewStore(path).Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if _, ok := latest["k"]; !ok {
		t.Error("observed snapshot was not persisted after the drain")
	}
}
