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

// persistWriter appends snapshots to a Store on its own goroutine, so the
// caller that observed a snapshot never waits on the store lock. Appends run one
// at a time in the order the snapshots were queued.
type persistWriter struct {
	store     *Store
	logf      func(string, ...any)
	onWritten func(persistJob)

	mu      sync.Mutex
	queue   []persistJob
	writing bool          // a queued job has been popped and is being appended
	idle    chan struct{} // closed when queue is empty and writing is false
	wake    chan struct{} // capacity one: queued work is pending
}

// newPersistWriter starts the writer goroutine for store. onWritten runs after
// every successful append.
func newPersistWriter(store *Store, logf func(string, ...any), onWritten func(persistJob)) *persistWriter {
	w := &persistWriter{
		store:     store,
		logf:      logf,
		onWritten: onWritten,
		idle:      make(chan struct{}),
		wake:      make(chan struct{}, 1),
	}
	close(w.idle)
	go w.run()
	return w
}

// enqueue queues job without blocking. When the queue is full the oldest pending
// job is dropped and logged: the newest observation carries the state a reader
// needs next, and the store's own dedupe skips most of the dropped writes
// anyway.
func (w *persistWriter) enqueue(job persistJob) {
	w.mu.Lock()
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
// ends.
func (w *persistWriter) drain(ctx context.Context) error {
	for {
		w.mu.Lock()
		if len(w.queue) == 0 && !w.writing {
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

// run appends queued jobs until the process exits.
func (w *persistWriter) run() {
	for range w.wake {
		for {
			job, ok := w.next()
			if !ok {
				break
			}
			if err := w.store.Append(job.snap, job.at); err != nil {
				w.logf("quota: persist %s: %v", job.snap.AccountKey, err)
			} else if w.onWritten != nil {
				w.onWritten(job)
			}
			w.finish()
		}
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
