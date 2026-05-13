package provider

import (
	"context"
	"fmt"

	"golang.org/x/sync/semaphore"
)

// Scheduler limits concurrent provider work.
type Scheduler struct {
	sem         *semaphore.Weighted
	parallelism int64
}

// NewScheduler creates a new provider scheduler with the given parallelism limit.
func NewScheduler(parallelism int) (*Scheduler, error) {
	if parallelism < 1 {
		return nil, fmt.Errorf("parallelism must be at least 1")
	}
	return &Scheduler{
		sem:         semaphore.NewWeighted(int64(parallelism)),
		parallelism: int64(parallelism),
	}, nil
}

// Acquire reserves one scheduler slot.
func (s *Scheduler) Acquire(ctx context.Context) error {
	if s == nil || s.sem == nil {
		return fmt.Errorf("scheduler is not initialized")
	}
	return s.sem.Acquire(ctx, 1)
}

// Release frees one scheduler slot.
func (s *Scheduler) Release() {
	if s == nil || s.sem == nil {
		return
	}
	s.sem.Release(1)
}

// Parallelism returns the configured scheduler width.
func (s *Scheduler) Parallelism() int {
	if s == nil {
		return 0
	}
	return int(s.parallelism)
}
