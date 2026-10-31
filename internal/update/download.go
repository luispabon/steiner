package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// maxDownloadBytes limits the size of downloaded binary assets to protect
	// against unbounded memory consumption. Steiner release assets are tens of MB;
	// this 256 MB limit accommodates realistic binaries.
	maxDownloadBytes = 256 * 1024 * 1024
)

// Network deadlines for update requests. Tests override these, so a test that
// writes them must not run in parallel with other tests in this package.
var (
	// assetDownloadTimeout bounds a whole binary asset download.
	assetDownloadTimeout = 15 * time.Minute
	// checksumDownloadTimeout bounds a whole checksums file download.
	checksumDownloadTimeout = 60 * time.Second
	// releaseFetchTimeout bounds a GitHub release API request.
	releaseFetchTimeout = 30 * time.Second
	// networkIdleTimeout bounds how long a request may make no progress,
	// including the wait for response headers.
	networkIdleTimeout = 30 * time.Second
)

// errNetworkStalled is the cancellation cause reported when a request makes no
// progress for networkIdleTimeout.
var errNetworkStalled = errors.New("network stalled")

// idleWatchdog cancels a request context when no progress is reported within a
// fixed interval. The timer starts armed, so it also covers the wait for
// response headers.
type idleWatchdog struct {
	idle     time.Duration
	cancel   context.CancelCauseFunc
	progress chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// newIdleWatchdog derives a context that is cancelled with errNetworkStalled
// when no progress is reported for idle. The caller must stop the watchdog when
// the request finishes.
func newIdleWatchdog(parent context.Context, idle time.Duration) (context.Context, *idleWatchdog) {
	ctx, cancel := context.WithCancelCause(parent)
	w := &idleWatchdog{
		idle:     idle,
		cancel:   cancel,
		progress: make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	go w.watch()
	return ctx, w
}

func (w *idleWatchdog) watch() {
	timer := time.NewTimer(w.idle)
	defer timer.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-timer.C:
			w.cancel(errNetworkStalled)
			return
		case <-w.progress:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(w.idle)
		}
	}
}

// reset records progress and restarts the idle timer.
func (w *idleWatchdog) reset() {
	select {
	case w.progress <- struct{}{}:
	default:
	}
}

// stop stops the watchdog goroutine and releases the derived context. It is
// safe to call more than once.
func (w *idleWatchdog) stop() {
	w.stopOnce.Do(func() {
		close(w.done)
		w.cancel(nil)
	})
}

// requestCause preserves err while also exposing, through errors.Is, the cause
// that cancelled ctx. When ctx is still live it returns err unchanged.
func requestCause(ctx context.Context, err error) error {
	cause := context.Cause(ctx)
	if cause == nil {
		return err
	}
	return &canceledRequestError{err: err, cause: cause}
}

// canceledRequestError pairs a transport error with the context cause that
// cancelled the request. Both are reachable by errors.Is, and the message only
// appends the cause when the transport error does not already state it.
type canceledRequestError struct {
	err   error
	cause error
}

func (e *canceledRequestError) Error() string {
	msg := e.err.Error()
	if strings.Contains(msg, e.cause.Error()) {
		return msg
	}
	return msg + ": " + e.cause.Error()
}

func (e *canceledRequestError) Unwrap() []error {
	return []error{e.err, e.cause}
}

// progressReader resets the idle watchdog whenever a read makes progress.
type progressReader struct {
	r        io.Reader
	watchdog *idleWatchdog
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		p.watchdog.reset()
	}
	return n, err
}

// downloadURL downloads the content at url with optional bearer token auth. The
// request ends when the caller's context ends or after networkIdleTimeout
// without progress.
func downloadURL(ctx context.Context, url, token string) ([]byte, error) {
	ctx, watchdog := newIdleWatchdog(ctx, networkIdleTimeout)
	defer watchdog.stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", requestCause(ctx, err))
	}
	watchdog.reset()
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(&progressReader{r: resp.Body, watchdog: watchdog}, maxDownloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", requestCause(ctx, err))
	}

	if len(body) > maxDownloadBytes {
		return nil, fmt.Errorf("download exceeded maximum size of %d bytes", maxDownloadBytes)
	}

	return body, nil
}

// downloadAsset downloads a release asset binary, bounded by
// assetDownloadTimeout.
func downloadAsset(ctx context.Context, url, token string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, assetDownloadTimeout)
	defer cancel()
	return downloadURL(ctx, url, token)
}

// downloadChecksums downloads the checksums file for a release, bounded by
// checksumDownloadTimeout.
func downloadChecksums(ctx context.Context, url, token string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, checksumDownloadTimeout)
	defer cancel()
	return downloadURL(ctx, url, token)
}
