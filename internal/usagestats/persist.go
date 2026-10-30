package usagestats

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// persistWriter coalesces usage deltas and writes them to the store on its own
// goroutine, so Recorder.Record never waits on the cross-process store lock.
// Every field of a delta is additive (request count and token sums), so pending
// deltas for one key are summed; the writer swaps the whole pending map for a
// fresh one and persists it with a single locked read, merge and atomic write.
// Under load that batches without a timer.
//
// The writer cannot cancel a store write blocked on the file lock: it stays
// blocked until the lock is released or the process exits. A drain that expires
// therefore abandons the wait while the writer may stay blocked, and the deltas
// still pending are lost, which the drain caller reports as a warning. Deltas
// keep being accepted while a drain runs: each one is queued normally, and only
// an expired deadline stops the wait. Close stops the writer; callers are
// expected to Drain first. Everything pending at that moment is dropped.
type persistWriter struct {
	store *store
	logf  func(string, ...any)

	stop      chan struct{}
	done      chan struct{} // closed when run returns
	wake      chan struct{} // capacity one: pending deltas wait to be written
	closeOnce sync.Once

	mu      sync.Mutex
	pending map[bucketKey]*bucket
	writing bool          // a swapped batch is being written
	stopped bool          // Close ran; new deltas are dropped
	idle    chan struct{} // closed when pending is empty and writing is false
}

// newPersistWriter starts the writer goroutine for store. A nil logf uses
// slog; the writer only calls logf outside its mutex.
func newPersistWriter(store *store, logf func(string, ...any)) *persistWriter {
	if logf == nil {
		logf = logPersist
	}
	w := &persistWriter{
		store:   store,
		logf:    logf,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		wake:    make(chan struct{}, 1),
		pending: make(map[bucketKey]*bucket),
		idle:    make(chan struct{}),
	}
	close(w.idle)
	go w.run()
	return w
}

// logPersist is the default persistence logger.
func logPersist(format string, args ...any) {
	slog.Warn(fmt.Sprintf(format, args...))
}

// add sums delta into the pending batch for key without blocking on the store.
// A delta arriving after Close is dropped and logged after the writer mutex is
// released, so logging never happens under a lock.
func (w *persistWriter) add(key bucketKey, delta bucket) {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		w.logf("usage stats: dropped delta for %s/%s/%s hour %d: persistence writer closed",
			key.providerAlias, key.providerType, key.backendModelID, key.hourUnix)
		return
	}
	if len(w.pending) == 0 && !w.writing {
		w.idle = make(chan struct{})
	}
	b, ok := w.pending[key]
	if !ok {
		b = &bucket{}
		w.pending[key] = b
	}
	b.Requests += delta.Requests
	b.InputTokens += delta.InputTokens
	b.CacheReadTokens += delta.CacheReadTokens
	b.CacheCreateTokens += delta.CacheCreateTokens
	b.CompletionTokens += delta.CompletionTokens
	w.mu.Unlock()

	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// drain waits until every pending and in-flight write has finished, until ctx
// ends, or until Close runs. A write cannot be cancelled, so an expired ctx
// abandons the wait while the writer may stay blocked on the store lock; the
// deltas still pending are then lost. Safe for concurrent and repeated use.
func (w *persistWriter) drain(ctx context.Context) error {
	for {
		w.mu.Lock()
		if w.stopped || (len(w.pending) == 0 && !w.writing) {
			w.mu.Unlock()
			return nil
		}
		idle := w.idle
		w.mu.Unlock()
		select {
		case <-idle:
		case <-w.stop:
			return nil
		case <-ctx.Done():
			return fmt.Errorf("drain usage stats persistence: %w", ctx.Err())
		}
	}
}

// close stops the writer goroutine and wakes drainers waiting on it. Once close
// returns, no new write starts: a claim not yet taken sees stopped, and a write
// already in flight still finishes (or stays blocked on the store lock until the
// lock is released or the process exits). Deltas pending at that moment are
// dropped and logged when the writer stops. Idempotent.
func (w *persistWriter) close() {
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.stopped = true
		w.mu.Unlock()
		close(w.stop)
	})
}

// run writes pending batches until Close runs, then closes done.
func (w *persistWriter) run() {
	defer close(w.done)
	for {
		select {
		case <-w.stop:
			w.abandon()
			return
		case <-w.wake:
		}
		for {
			next := w.next()
			if next.stopped {
				w.abandon()
				return
			}
			if !next.claimed {
				break
			}
			w.flush(next.batch)
			w.finish()
		}
	}
}

// flush writes one claimed batch and logs a failure. It runs with no writer
// mutex held, so logging never happens under a lock.
func (w *persistWriter) flush(batch map[bucketKey]*bucket) {
	if err := w.store.writeBatch(batch); err != nil {
		w.logf("usage stats: persist %d buckets: %v", len(batch), err)
	}
}

// batchClaim is what next found under the writer mutex.
type batchClaim struct {
	// batch is the claimed deltas; valid only when claimed is true.
	batch map[bucketKey]*bucket
	// claimed is true when a batch was taken and its write is now in flight.
	claimed bool
	// stopped is true when Close has run, so no batch may be claimed.
	stopped bool
}

// next claims the pending deltas as one batch and marks its write in flight.
// The claim is taken under the writer mutex with stopped still false, and a
// claim is the only thing that starts a write, so a Close that has completed by
// the time the mutex is acquired prevents any later write instead of racing the
// run loop's stop check.
func (w *persistWriter) next() batchClaim {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return batchClaim{stopped: true}
	}
	if len(w.pending) == 0 {
		return batchClaim{}
	}
	batch := w.pending
	w.pending = make(map[bucketKey]*bucket)
	w.writing = true
	return batchClaim{batch: batch, claimed: true}
}

// finish clears the in-flight marker and releases drainers once nothing is left
// to write.
func (w *persistWriter) finish() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writing = false
	if len(w.pending) == 0 {
		close(w.idle)
	}
}

// abandon drops the pending deltas and releases drainers. It runs only with no
// write in flight, so the idle channel is open exactly when deltas were
// pending.
func (w *persistWriter) abandon() {
	w.mu.Lock()
	dropped := len(w.pending)
	w.pending = nil
	if dropped > 0 {
		close(w.idle)
	}
	w.mu.Unlock()
	if dropped > 0 {
		w.logf("usage stats: dropped %d pending buckets: persistence writer closed", dropped)
	}
}
