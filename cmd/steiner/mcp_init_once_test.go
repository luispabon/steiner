package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestMCPInitQueuedWaitHonoursContextCancellation proves a caller queued behind
// a stalled initialization returns as soon as its own context ends, without
// waiting for the running attempt, and that the initialization still completes
// afterwards.
func TestMCPInitQueuedWaitHonoursContextCancellation(t *testing.T) {
	t.Parallel()
	init := &mcpInitOnce{}
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	init.attempt = func(context.Context, cliRuntime) error {
		startedOnce.Do(func() { close(started) })
		<-release
		return nil
	}

	holder := make(chan error, 1)
	go func() { holder <- init.wait(context.Background(), cliRuntime{}) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first attempt never started")
	}

	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() { queued <- init.wait(ctx, cliRuntime{}) }()
	// The queued caller stays blocked while the first attempt runs.
	select {
	case err := <-queued:
		t.Fatalf("queued caller returned %v before its context was cancelled", err)
	case <-time.After(20 * time.Millisecond):
	}

	cancelledAt := time.Now()
	cancel()
	select {
	case err := <-queued:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued caller error = %v, want context.Canceled", err)
		}
		if elapsed := time.Since(cancelledAt); elapsed > time.Second {
			t.Fatalf("queued caller returned %v after cancellation, want a prompt return", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued caller did not return after its context was cancelled")
	}

	close(release)
	if err := <-holder; err != nil {
		t.Fatalf("first wait = %v, want nil after the attempt completed", err)
	}
	// A later request observes the completed initialization.
	if err := init.wait(context.Background(), cliRuntime{}); err != nil {
		t.Fatalf("wait after completion = %v, want nil", err)
	}
}

// TestMCPInitConcurrentWaitersRunOneAttempt proves concurrent callers never run
// two attempts at once: one caller performs the initialization and the rest
// observe its result.
func TestMCPInitConcurrentWaitersRunOneAttempt(t *testing.T) {
	t.Parallel()
	init := &mcpInitOnce{}
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	var mu sync.Mutex
	calls, active, maxActive := 0, 0, 0
	init.attempt = func(context.Context, cliRuntime) error {
		mu.Lock()
		calls++
		active++
		maxActive = max(maxActive, active)
		mu.Unlock()
		startedOnce.Do(func() { close(started) })
		<-release
		mu.Lock()
		active--
		mu.Unlock()
		return nil
	}

	const callers = 8
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- init.wait(context.Background(), cliRuntime{})
		}()
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("no attempt started")
	}
	// Give the remaining callers time to queue behind the running attempt.
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("wait = %v, want nil", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("attempts = %d, want exactly 1", calls)
	}
	if maxActive != 1 {
		t.Fatalf("overlapping attempts = %d, want 1", maxActive)
	}
}
