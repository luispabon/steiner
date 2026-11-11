package oauth

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"golang.org/x/oauth2"
)

const (
	// saveRetryAttempts bounds the save-only retries that follow a save failure,
	// so a token file that stays unwritable is not rewritten on every call. Only
	// the next refresh tries the save again after they are used up.
	saveRetryAttempts = 3

	// saveRetryBaseDelay is the pause before the first save-only retry; each
	// later pause doubles it.
	saveRetryBaseDelay = 30 * time.Second

	// saveRetryLockWait bounds how long a save-only retry waits for the token
	// lock. A cache hit must not inherit the refresh deadline when another
	// process holds the lock, so a busy lock skips the retry instead.
	saveRetryLockWait = 100 * time.Millisecond
)

// errSaveRetryLockBusy reports a save-only retry that could not take the token
// lock in time, so it wrote nothing and the retained token stays cached.
var errSaveRetryLockBusy = errors.New("save retry skipped: token lock busy")

// saveRetryDelay returns the pause before the retry that follows the given
// number of failed saves.
func saveRetryDelay(failed int) time.Duration {
	delay := saveRetryBaseDelay
	for i := 1; i < failed && i < saveRetryAttempts; i++ {
		delay *= 2
	}
	return delay
}

// saveRetryDueLocked reports whether the cached token is retained and a
// save-only retry is due at now.
func (e *authEntry) saveRetryDueLocked(now time.Time) bool {
	return !e.persisted && e.saveAttempts < saveRetryAttempts && !now.Before(e.saveAfter)
}

// scheduleSaveRetryLocked counts one more failed save and sets the earliest
// time of the next retry.
func (e *authEntry) scheduleSaveRetryLocked(now time.Time) {
	e.saveAttempts++
	e.awaitSaveRetryLocked(now)
}

// awaitSaveRetryLocked sets the earliest time of the next save-only retry after
// the pause that follows the saves already attempted.
func (e *authEntry) awaitSaveRetryLocked(now time.Time) {
	e.saveAfter = now.Add(saveRetryDelay(e.saveAttempts + 1))
}

// pendingSaveLocked returns the retained token and its stamp when a save-only
// retry is due at now and stamp still describes the version the token was
// refreshed from. Any other case needs a refresh: the file moved on, the token
// needs refreshing, or the retries are exhausted.
func (e *authEntry) pendingSaveLocked(now time.Time, stamp fileStamp) (*oauth2.Token, fileStamp, bool) {
	if !e.valid || e.token == nil || !e.stamp.equal(stamp) || !e.saveRetryDueLocked(now) {
		return nil, fileStamp{}, false
	}
	if tokenNeedsRefresh(e.token, now) {
		return nil, fileStamp{}, false
	}
	return cloneToken(e.token), e.stamp, true
}

// saveRetained publishes the result of one save-only retry of a retained token
// to the entry's cache and to the flight's waiters. The token endpoint is not
// contacted: the refresh that produced the token already rotated its refresh
// token, so only the write is missing.
//
// The retry takes the token lock for at most saveRetryLockWait, then re-checks
// the file before writing. Its outcomes are:
//   - written: the cache publishes the token read back from disk with the stamp
//     of that version, marked persisted, and the waiters get it.
//   - lock busy: nothing is written and no attempt is used; the cache keeps the
//     retained token, the waiters get it, and the retry is tried again after the
//     current pause.
//   - failed: the cache keeps the retained token, the waiters get it, and the
//     attempt count moves the next retry back. The error is not reported,
//     because the callers hold a valid token and the refresh that produced it
//     already reported the save failure that retained the token.
//   - file replaced: the flight finishes as a refresh, so the cache publishes
//     the version that won and the waiters get a usable token.
//   - file deleted: the cache is cleared and the waiters get ErrNoToken, leaving
//     the file deleted.
func (r *AuthResolver) saveRetained(entry *authEntry, flight *authFlight, file tokenFile, conf *oauth2.Config, httpClient *http.Client, token *oauth2.Token, stamp fileStamp) {
	current, currentStamp, saved, err := r.saveRetainedToken(entry.store, file, token, stamp)

	if err == nil && (!saved || tokenNeedsRefresh(current, r.clock())) {
		// The file holds another version, so the retry has nothing to write: the
		// refresh adopts that version, or refreshes it when it is not usable.
		r.refresh(entry, flight, file, conf, httpClient)
		return
	}

	entry.mu.Lock()
	switch {
	case errors.Is(err, ErrNoToken):
		// The user removed the credential while the retry waited: publish no
		// token, so the cache is empty and the file stays deleted.
		entry.clearLocked()
	case errors.Is(err, errSaveRetryLockBusy):
		// The lock stayed busy, so no save failed: keep the retained token so
		// the waiters are served it, and try again after the current pause.
		entry.awaitSaveRetryLocked(r.clock())
	case err != nil:
		// The retry failed: keep the retained token so the waiters are served it
		// and move the next retry back.
		entry.scheduleSaveRetryLocked(r.clock())
	default:
		// The write landed: publish the version read back from disk with the
		// stamp that describes it.
		entry.setLocked(current, currentStamp, true)
	}
	entry.flight = nil
	entry.mu.Unlock()

	served := token
	if err == nil {
		served = current
	}
	flight.token = cloneToken(served)
	if errors.Is(err, ErrNoToken) {
		flight.err = ErrNoToken
	}
	close(flight.done)
}

// saveRetainedToken writes token under the token lock when the file still holds
// the version stamp describes. It waits at most saveRetryLockWait for the lock,
// reporting errSaveRetryLockBusy when another holder keeps it; any other lock
// error is real and backs off like a failed save. It returns the credential and
// stamp of the version on disk after a successful write; saved is false without
// an error when the file changed or disappeared first, leaving that version
// untouched.
func (r *AuthResolver) saveRetainedToken(store *TokenStore, file tokenFile, token *oauth2.Token, stamp fileStamp) (current *oauth2.Token, currentStamp fileStamp, saved bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), saveRetryLockWait)
	defer cancel()

	unlock, err := r.acquireLock(ctx, store.path)
	if err != nil {
		// The lock can fail because the lock file or its directory is gone, which
		// also means the token is gone: report that instead of retrying a save
		// that can never land.
		if _, statErr := file.stat(); errors.Is(statErr, fs.ErrNotExist) {
			return nil, fileStamp{}, false, ErrNoToken
		}
		// Only the short wait expiring means another process still holds the
		// lock; an unopenable or unreadable lock file is a real failure that uses
		// an attempt and backs off.
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fileStamp{}, false, fmt.Errorf("%w: %w", errSaveRetryLockBusy, err)
		}
		return nil, fileStamp{}, false, err
	}
	defer unlock()

	onDisk, err := file.stat()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fileStamp{}, false, nil
		}
		return nil, fileStamp{}, false, statError(err)
	}
	if !onDisk.equal(stamp) {
		// The file holds another version, so saving the retained token would
		// overwrite a newer external write.
		return nil, fileStamp{}, false, nil
	}
	if err := store.Save(token); err != nil {
		return nil, fileStamp{}, false, fmt.Errorf("save retained token: %w", err)
	}
	// Re-read after the write instead of statting it, so the cached stamp is
	// paired with the token it describes and a replacement that lands right
	// after the write wins.
	current, currentStamp, err = readStable(file)
	if err != nil {
		return nil, fileStamp{}, false, err
	}
	return current, currentStamp, true, nil
}
