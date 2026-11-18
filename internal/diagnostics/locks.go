package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/luispabon/steiner/internal/filelock"
)

// streamLockTimeout bounds how long a protected mutation waits for another
// process's stream lock. Diagnostics is best effort, so a skipped record costs
// less than an append parked behind a peer that holds the lock.
const streamLockTimeout = 100 * time.Millisecond

// acquireStreamFileLock takes a stream's sibling lock file. Tests replace it to
// exercise lock failures.
var acquireStreamFileLock = filelock.Acquire

// streamLockPath names the stable lock file guarding one stream. Callers never
// delete it: every cooperating process must lock the same inode, and a lock
// file that vanished would leave two processes excluding nothing.
func streamLockPath(dir string, kind Kind) string {
	return filepath.Join(dir, kind.fileName()+".lock")
}

// lockStream acquires the lock guarding kind's stream, reporting false when no
// usable lock is available. Every mutation that touches the stream file holds
// it, so retention, tail repair, rotation and appends in cooperating processes
// serialize instead of interleaving.
//
// A false result has already warned through w.warn, and the caller must skip the
// mutation rather than run it unprotected. Caller must hold the writer's mutex,
// or be New before the writer is shared.
func (w *Writer) lockStream(action string, kind Kind) (*filelock.Lock, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), streamLockTimeout)
	defer cancel()

	lock, err := acquireStreamFileLock(ctx, streamLockPath(w.dir, kind))
	if err != nil {
		w.warn(action, fmt.Errorf("lock diagnostics stream: %w", err))
		return nil, false
	}
	if lock == nil {
		w.warn(action, errors.New("lock diagnostics stream: no lock returned"))
		return nil, false
	}
	if lock.Unsupported() {
		warning := lock.Warning()
		// Best-effort: an unsupported lock holds no kernel lock, but release it
		// so the caller never keeps a descriptor open.
		_ = lock.Release()
		w.warn(action, fmt.Errorf("lock diagnostics stream: %s", warning))
		return nil, false
	}
	return lock, true
}
