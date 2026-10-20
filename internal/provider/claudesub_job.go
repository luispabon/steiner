package provider

import (
	"context"
	"fmt"
	"sync"
)

// claudeSubJob is a once-started asynchronous cleanup owned by a pool or one of
// its sessions, with a durable completion and result. start launches its work
// exactly once in its own goroutine and returns immediately; the result is
// published and done is closed together under the pool mutex, so a caller that
// observes done always sees the final result and the shutdown coordinator can
// linearize "is this job settled?" against result publication. A cleanup job
// also records its error in the pool's accumulated ledger before it signals
// done, so a failure known before the close result is published is joined into
// it, while one discovered afterwards stays durably owned without retroactively
// changing the returned result.
type claudeSubJob struct {
	pool    *ClaudeSubscriptionPool
	once    sync.Once
	done    chan struct{}
	err     error
	started bool // guarded by pool.mu; set when the job is registered
	record  bool // record err in the pool's cleanup ledger on completion
}

// newCleanupJob returns a job whose error is recorded in the pool's cleanup
// ledger on completion.
func (p *ClaudeSubscriptionPool) newCleanupJob() *claudeSubJob {
	return &claudeSubJob{pool: p, done: make(chan struct{}), record: true}
}

// newTrackedJob returns a job that is tracked for unsettled detection but whose
// result is not recorded: the shared CLI lookup reports its outcome to acquire
// callers, not as a pool cleanup error.
func (p *ClaudeSubscriptionPool) newTrackedJob() *claudeSubJob {
	return &claudeSubJob{pool: p, done: make(chan struct{})}
}

// start launches the job's work once. It returns immediately and never blocks
// on the work: a later caller observes the durable result through wait instead
// of waiting inside once.
func (j *claudeSubJob) start(fn func() error) {
	j.once.Do(func() {
		j.pool.trackJob(j)
		go func() { j.finish(fn()) }()
	})
}

// trackJob registers a started job under the pool mutex so final result
// assembly can tell whether it is unsettled. It never blocks on the job.
func (p *ClaudeSubscriptionPool) trackJob(j *claudeSubJob) {
	p.mu.Lock()
	j.started = true
	p.cleanupJobs = append(p.cleanupJobs, j)
	p.mu.Unlock()
}

// finish publishes a job's result and closes its done channel atomically under
// the pool mutex. A cleanup job's error is recorded before done closes, so a
// result known before the close result is published is always joined and one
// discovered afterwards is durably owned without changing it.
func (j *claudeSubJob) finish(err error) {
	p := j.pool
	if p.beforeJobRecord != nil {
		p.beforeJobRecord(err)
	}
	p.mu.Lock()
	j.err = err
	if j.record && err != nil {
		p.cleanupErrs = append(p.cleanupErrs, err)
	}
	close(j.done)
	p.mu.Unlock()
}

// wait blocks until the job completes or ctx is done. It returns the job result
// once it completed, or incomplete wrapping ctx.Err() when the deadline expired
// first (the job keeps running).
func (j *claudeSubJob) wait(ctx context.Context, incomplete error) error {
	select {
	case <-j.done:
		return j.err
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", incomplete, ctx.Err())
	}
}
