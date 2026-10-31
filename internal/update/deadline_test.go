package update

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// setNetworkTimeouts overrides the package network deadlines for a non-parallel
// test and restores them when it finishes.
func setNetworkTimeouts(t *testing.T, asset, checksum, release, idle time.Duration) {
	t.Helper()
	prevAsset, prevChecksum, prevRelease, prevIdle := assetDownloadTimeout, checksumDownloadTimeout, releaseFetchTimeout, networkIdleTimeout
	assetDownloadTimeout, checksumDownloadTimeout, releaseFetchTimeout, networkIdleTimeout = asset, checksum, release, idle
	t.Cleanup(func() {
		assetDownloadTimeout, checksumDownloadTimeout, releaseFetchTimeout, networkIdleTimeout = prevAsset, prevChecksum, prevRelease, prevIdle
	})
}

// slowBodyServer serves one byte every interval, flushing each write, until the
// request context ends. The steady progress means only an overall deadline can
// end the transfer.
func slowBodyServer(t *testing.T, interval time.Duration) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter does not support flushing")
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				if _, err := w.Write([]byte("x")); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}))
}

// stalledServer writes the given prefix, flushes it, then blocks until the
// client gives up. An empty prefix sends nothing, so the client waits for the
// response headers.
func stalledServer(t *testing.T, prefix string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if prefix != "" {
			if _, err := w.Write([]byte(prefix)); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		<-r.Context().Done()
	}))
}

// chunkedServer writes payload in roughly equal chunks, waiting interval
// between writes, so a transfer can outlast the idle timeout while still making
// progress.
func chunkedServer(t *testing.T, payload []byte, chunks int, interval time.Duration) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter does not support flushing")
			return
		}
		size := (len(payload) + chunks - 1) / chunks
		for start := 0; start < len(payload); start += size {
			end := min(start+size, len(payload))
			if _, err := w.Write(payload[start:end]); err != nil {
				return
			}
			flusher.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(interval):
			}
		}
	}))
}

func TestDownload_OverallDeadline(t *testing.T) {
	const (
		overall  = 400 * time.Millisecond
		progress = 10 * time.Millisecond
	)

	tests := []struct {
		name     string
		download func(ctx context.Context, url, token string) ([]byte, error)
		setLimit func(t *testing.T)
	}{
		{
			name:     "asset",
			download: downloadAsset,
			setLimit: func(t *testing.T) { setNetworkTimeouts(t, overall, time.Hour, time.Hour, time.Hour) },
		},
		{
			name:     "checksums",
			download: downloadChecksums,
			setLimit: func(t *testing.T) { setNetworkTimeouts(t, time.Hour, overall, time.Hour, time.Hour) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The idle timeout is far longer than the transfer, so only the
			// overall deadline can end it.
			tt.setLimit(t)
			server := slowBodyServer(t, progress)
			defer server.Close()

			defer saveHTTPClient()()
			httpClient = newTestClient(server.URL)

			_, err := tt.download(context.Background(), server.URL+"/asset", "")
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("download error = %v, want context.DeadlineExceeded", err)
			}
			if strings.Count(err.Error(), context.DeadlineExceeded.Error()) != 1 {
				t.Errorf("download error = %q, want the deadline cause stated once", err)
			}
		})
	}
}

func TestDownloadURL_StallAfterHeaders(t *testing.T) {
	setNetworkTimeouts(t, time.Hour, time.Hour, time.Hour, 50*time.Millisecond)

	server := stalledServer(t, "partial")
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := downloadURL(context.Background(), server.URL, "")
	if !errors.Is(err, errNetworkStalled) {
		t.Fatalf("downloadURL error = %v, want errNetworkStalled", err)
	}
}

func TestDownloadURL_StallWaitingForHeaders(t *testing.T) {
	setNetworkTimeouts(t, time.Hour, time.Hour, time.Hour, 50*time.Millisecond)

	server := stalledServer(t, "")
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := downloadURL(context.Background(), server.URL, "")
	if !errors.Is(err, errNetworkStalled) {
		t.Fatalf("downloadURL error = %v, want errNetworkStalled", err)
	}
	if !strings.Contains(err.Error(), server.URL) {
		t.Errorf("downloadURL error = %q, want the original url.Error with its URL", err)
	}
}

func TestFetchRelease_OverallDeadline(t *testing.T) {
	setNetworkTimeouts(t, time.Hour, time.Hour, 400*time.Millisecond, time.Hour)

	server := slowBodyServer(t, 10*time.Millisecond)
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := fetchLatestRelease(context.Background(), "owner", "repo", "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("fetchLatestRelease error = %v, want context.DeadlineExceeded", err)
	}
}

func TestFetchRelease_Stall(t *testing.T) {
	setNetworkTimeouts(t, time.Hour, time.Hour, time.Hour, 50*time.Millisecond)

	server := stalledServer(t, `{"tag_name":`)
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := fetchLatestRelease(context.Background(), "owner", "repo", "")
	if !errors.Is(err, errNetworkStalled) {
		t.Fatalf("fetchLatestRelease error = %v, want errNetworkStalled", err)
	}
}

func TestNetworkRequests_RespectCallerCancellation(t *testing.T) {
	setNetworkTimeouts(t, time.Hour, time.Hour, time.Hour, time.Hour)

	server := stalledServer(t, "")
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	tests := []struct {
		name string
		call func(ctx context.Context) error
	}{
		{
			name: "download",
			call: func(ctx context.Context) error {
				_, err := downloadAsset(ctx, server.URL+"/asset", "")
				return err
			},
		},
		{
			name: "release",
			call: func(ctx context.Context) error {
				_, err := fetchLatestRelease(ctx, "owner", "repo", "")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				time.Sleep(30 * time.Millisecond)
				cancel()
			}()

			err := tt.call(ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("request error = %v, want context.Canceled", err)
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errNetworkStalled) {
				t.Fatalf("request error = %v, want caller cancellation to take precedence", err)
			}
			if !strings.Contains(err.Error(), server.URL) {
				t.Errorf("request error = %q, want the original url.Error with its URL", err)
			}
		})
	}
}

func TestDownloadURL_AlreadyCancelledContext(t *testing.T) {
	setNetworkTimeouts(t, time.Hour, time.Hour, time.Hour, time.Hour)

	server := stalledServer(t, "")
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := downloadURL(ctx, server.URL, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("downloadURL error = %v, want context.Canceled", err)
	}
}

func TestNetworkRequests_SlowProgressSucceeds(t *testing.T) {
	const (
		idle     = 500 * time.Millisecond
		interval = 50 * time.Millisecond
		chunks   = 12
	)
	// The transfer outlasts the idle timeout, so it only completes because every
	// chunk resets the watchdog.
	setNetworkTimeouts(t, time.Hour, time.Hour, time.Hour, idle)

	defer saveHTTPClient()()

	t.Run("download", func(t *testing.T) {
		payload := []byte(strings.Repeat("x", chunks))
		server := chunkedServer(t, payload, chunks, interval)
		defer server.Close()
		httpClient = newTestClient(server.URL)

		got, err := downloadURL(context.Background(), server.URL, "")
		if err != nil {
			t.Fatalf("downloadURL: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("downloadURL body = %q, want %q", got, payload)
		}
	})

	t.Run("release", func(t *testing.T) {
		// Padded so the payload spans the full chunk count and the last write
		// lands after the idle timeout.
		payload := []byte(`{"tag_name":"v1.2.3","assets":[]}` + strings.Repeat(" ", 15))
		server := chunkedServer(t, payload, chunks, interval)
		defer server.Close()
		httpClient = newTestClient(server.URL)

		rel, err := fetchLatestRelease(context.Background(), "owner", "repo", "")
		if err != nil {
			t.Fatalf("fetchLatestRelease: %v", err)
		}
		if rel.TagName != "v1.2.3" {
			t.Errorf("TagName = %q, want %q", rel.TagName, "v1.2.3")
		}
	})
}
