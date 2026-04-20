package provider

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSchedulerBlocksBeyondParallelism(t *testing.T) {
	sched, err := NewScheduler(1)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}

	if err := sched.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer sched.Release()

	acquired := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		err := sched.Acquire(context.Background())
		if err == nil {
			close(acquired)
		}
		errCh <- err
	}()

	select {
	case <-acquired:
		t.Fatal("second acquire succeeded before release")
	case <-time.After(50 * time.Millisecond):
	}

	sched.Release()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Acquire() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second acquire did not complete after release")
	}
}

func TestSchedulerAcquireHonorsContextCancellation(t *testing.T) {
	sched, err := NewScheduler(1)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}

	if err := sched.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer sched.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	err = sched.Acquire(ctx)
	if err == nil {
		t.Fatal("Acquire() error = nil, want cancellation error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire() error = %v, want context cancellation", err)
	}
}
