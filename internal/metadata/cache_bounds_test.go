package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRefreshRejectsOversizeBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pad":"`))
		chunk := []byte(strings.Repeat("a", 1<<20))
		for i := 0; i < maxResponseBytes/(1<<20)+2; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
		_, _ = w.Write([]byte(`"}`))
	}))
	defer srv.Close()
	c := newTestCache(t)
	c.HTTPClient = &http.Client{Transport: &redirectTransport{target: srv.URL}}
	if err := c.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh() error = nil, want oversize rejection")
	}
	if _, err := os.Stat(c.CachePath()); !os.IsNotExist(err) {
		t.Fatalf("cache file written for oversize body: %v", err)
	}
}

func TestRefreshHonorsDeadlineOnHungServer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	c := newTestCache(t)
	c.HTTPClient = &http.Client{Transport: &redirectTransport{target: srv.URL}}
	c.refreshTimeout = 100 * time.Millisecond
	start := time.Now()
	_ = c.Refresh(context.Background())
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Refresh() took %v, want bounded by timeout", elapsed)
	}
}

func TestDefaultClientHasTimeout(t *testing.T) {
	c := newTestCache(t)
	if got := c.httpClient().Timeout; got <= 0 {
		t.Fatalf("default client Timeout = %v, want > 0", got)
	}
}

func TestCacheDirPermissions(t *testing.T) {
	c := &Cache{Dir: t.TempDir() + "/sub"}
	if err := atomicWrite(c.CachePath(), []byte("{}")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(c.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir perm = %o, want 700", got)
	}
}
