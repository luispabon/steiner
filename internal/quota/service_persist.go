package quota

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// persistQueueDepth bounds how many snapshots wait for the store writer. A full
// queue drops the oldest pending snapshot, so the writer always ends on the
// newest state instead of a stale one.
const persistQueueDepth = 32

// persistJob is one snapshot queued for the store writer. at is the service
// clock reading when the snapshot was committed, which Store.Append uses for
// its dedupe window and retention cutoff.
type persistJob struct {
	snap Snapshot
	at   time.Time
}

// persistWriter appends snapshots to a Store on its own goroutine, so the caller
// that observed a snapshot never waits on the store lock. Appends run one at a
// time in the order the snapshots were queued.
//
// The writer cannot cancel a Store.Append blocked on the file lock: it stays
// blocked until the lock is released or the process exits. A drain that expires
// therefore abandons the wait while the writer may stay blocked (it is reaped at
// exit), and the snapshots still queued or in flight are lost, which the drain
// caller reports as a warning. Observations keep being accepted after a drain
// starts: each one is queued normally, and only an expired deadline stops the
// wait. Close stops the writer; callers are expected to Drain first.
type persistWriter struct {
	store    *Store
	logf     func(string, ...any)
	onAppend func(job persistJob, written bool, matched Snapshot)

	closeOnce sync.Once
	stop      chan struct{}

	mu      sync.Mutex
	queue   []persistJob
	writing bool          // a queued job has been popped and is being appended
	stopped bool          // Close ran; new jobs are dropped
	idle    chan struct{} // closed when queue is empty and writing is false
	wake    chan struct{} // capacity one: queued work is pending
}

// newPersistWriter starts the writer goroutine for store. onAppend runs after
// every append that reached the store: written reports whether the file was
// rewritten, and matched is the stored snapshot a skipped dedupe matched.
func newPersistWriter(store *Store, logf func(string, ...any), onAppend func(persistJob, bool, Snapshot)) *persistWriter {
	w := &persistWriter{
		store:    store,
		logf:     logf,
		onAppend: onAppend,
		stop:     make(chan struct{}),
		idle:     make(chan struct{}),
		wake:     make(chan struct{}, 1),
	}
	close(w.idle)
	go w.run()
	return w
}

// enqueue queues job without blocking. When the queue is full the oldest pending
// job is dropped and logged: the newest observation carries the state a reader
// needs next, and the store's own dedupe skips most of the dropped writes
// anyway. Jobs arriving after Close are dropped and logged.
func (w *persistWriter) enqueue(job persistJob) {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		w.logf("quota: dropped snapshot for %s fetched %s: persistence writer closed", job.snap.AccountKey, job.snap.FetchedAt)
		return
	}
	var dropped persistJob
	overflowed := len(w.queue) >= persistQueueDepth
	if overflowed {
		dropped = w.queue[0]
		w.queue = w.queue[1:]
	}
	if len(w.queue) == 0 && !w.writing {
		w.idle = make(chan struct{})
	}
	w.queue = append(w.queue, job)
	w.mu.Unlock()

	if overflowed {
		w.logf("quota: dropped queued snapshot for %s fetched %s: persist queue full", dropped.snap.AccountKey, dropped.snap.FetchedAt)
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// drain waits until every queued and in-flight append has finished, or until ctx
// ends. Appends cannot be cancelled, so an expired ctx abandons the wait while the
// writer may stay blocked on the store lock; queued snapshots are then lost. The
// caller reports that as a warning. After Close, drain stops waiting because
// nothing more will be written. Safe for concurrent and repeated use.
func (w *persistWriter) drain(ctx context.Context) error {
	for {
		w.mu.Lock()
		if w.stopped || (len(w.queue) == 0 && !w.writing) {
			w.mu.Unlock()
			return nil
		}
		idle := w.idle
		w.mu.Unlock()
		select {
		case <-idle:
		case <-ctx.Done():
			return fmt.Errorf("drain quota persistence: %w", ctx.Err())
		}
	}
}

// close stops the writer goroutine. Jobs queued at that moment are dropped and
// logged, and an append already in flight still finishes (or stays blocked on
// the store lock until the lock is released or the process exits). Idempotent.
func (w *persistWriter) close() {
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.stopped = true
		w.mu.Unlock()
		close(w.stop)
	})
}

// run appends queued jobs until Close runs.
func (w *persistWriter) run() {
	for {
		select {
		case <-w.stop:
			w.abandon()
			return
		case <-w.wake:
		}
		for {
			job, ok := w.next()
			if !ok {
				break
			}
			w.appendJob(job)
			w.finish()
			select {
			case <-w.stop:
				w.abandon()
				return
			default:
			}
		}
	}
}

// appendJob appends one job and hands the outcome to onAppend, which runs
// without w.mu held so it may take the service lock.
func (w *persistWriter) appendJob(job persistJob) {
	written, matched, err := w.store.append(job.snap, job.at)
	if err != nil {
		w.logf("quota: persist %s: %v", job.snap.AccountKey, err)
		return
	}
	if w.onAppend != nil {
		w.onAppend(job, written, matched)
	}
}

// next pops the oldest queued job and marks its append in flight.
func (w *persistWriter) next() (persistJob, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.queue) == 0 {
		return persistJob{}, false
	}
	job := w.queue[0]
	w.queue = w.queue[1:]
	w.writing = true
	return job, true
}

// finish clears the in-flight marker and releases drainers once nothing is left
// to append.
func (w *persistWriter) finish() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writing = false
	if len(w.queue) == 0 {
		close(w.idle)
	}
}

// abandon drops the queue and releases drainers. It runs only with no append in
// flight, so the idle channel is open exactly when jobs were queued.
func (w *persistWriter) abandon() {
	w.mu.Lock()
	dropped := len(w.queue)
	w.queue = nil
	if dropped > 0 {
		close(w.idle)
	}
	w.mu.Unlock()
	if dropped > 0 {
		w.logf("quota: dropped %d queued snapshots: persistence writer closed", dropped)
	}
}
