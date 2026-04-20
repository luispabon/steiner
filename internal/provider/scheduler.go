package provider

import (
	"context"
	"fmt"

	"golang.org/x/sync/semaphore"
)

type Scheduler struct {
	sem         *semaphore.Weighted
	parallelism int64
}

func NewScheduler(parallelism int) (*Scheduler, error) {
	if parallelism < 1 {
		return nil, fmt.Errorf("parallelism must be at least 1")
	}
	return &Scheduler{
		sem:         semaphore.NewWeighted(int64(parallelism)),
		parallelism: int64(parallelism),
	}, nil
}

func (s *Scheduler) Acquire(ctx context.Context) error {
	if s == nil || s.sem == nil {
		return fmt.Errorf("scheduler is not initialized")
	}
	return s.sem.Acquire(ctx, 1)
}

func (s *Scheduler) Release() {
	if s == nil || s.sem == nil {
		return
	}
	s.sem.Release(1)
}

func (s *Scheduler) Parallelism() int {
	if s == nil {
		return 0
	}
	return int(s.parallelism)
}
