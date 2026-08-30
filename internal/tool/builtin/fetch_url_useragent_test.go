package builtin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestFetchURLSendsIdentifyingUserAgent(t *testing.T) {
	t.Run("HEAD preflight and wonton fetch", func(t *testing.T) {
		html := `<html><body><main><p>` +
			strings.Repeat("Real article prose about the subject matter. ", 10) +
			`</p></main></body></html>`

		var headUA, getUA atomic.Value
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			if r.Method == http.MethodHead {
				headUA.Store(r.Header.Get("User-Agent"))
				w.WriteHeader(http.StatusOK)
				return
			}
			getUA.Store(r.Header.Get("User-Agent"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(html))
		}))
		defer server.Close()

		workDir := t.TempDir()
		policy := tool.NewPathPolicy(workDir, config.PathsConfig{})
		env := Env{
			WorkDir:    workDir,
			PathPolicy: &policy,
			httpClient: func() *http.Client { return server.Client() },
		}

		if _, err := NewFetchURLTool(env).Handler(context.Background(), map[string]any{"url": server.URL}); err != nil {
			t.Fatalf("handler: %v", err)
		}
		if ua, _ := headUA.Load().(string); ua != fetchUserAgent {
			t.Errorf("HEAD User-Agent = %q, want %q", ua, fetchUserAgent)
		}
		if ua, _ := getUA.Load().(string); ua != fetchUserAgent {
			t.Errorf("wonton fetch User-Agent = %q, want %q", ua, fetchUserAgent)
		}
	})

	t.Run("wonton fallback fetch", func(t *testing.T) {
		// No <main> container, forcing the main-content extraction fallback
		// to a full-document re-fetch (see TestFetchURLMainContentFallback).
		html := `<html><body><aside><p>` +
			strings.Repeat("Aside prose content that only exists outside main. ", 10) +
			`</p></aside></body></html>`

		var getUAs []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			getUAs = append(getUAs, r.Header.Get("User-Agent"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(html))
		}))
		defer server.Close()

		workDir := t.TempDir()
		policy := tool.NewPathPolicy(workDir, config.PathsConfig{})
		env := Env{
			WorkDir:    workDir,
			PathPolicy: &policy,
			httpClient: func() *http.Client { return server.Client() },
		}

		if _, err := NewFetchURLTool(env).Handler(context.Background(), map[string]any{"url": server.URL}); err != nil {
			t.Fatalf("handler: %v", err)
		}
		if len(getUAs) != 2 {
			t.Fatalf("GET requests = %d, want 2 (fallback re-fetches once)", len(getUAs))
		}
		for i, ua := range getUAs {
			if ua != fetchUserAgent {
				t.Errorf("GET[%d] User-Agent = %q, want %q", i, ua, fetchUserAgent)
			}
		}
	})

	t.Run("raw text GET", func(t *testing.T) {
		var getUA atomic.Value
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			getUA.Store(r.Header.Get("User-Agent"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("plain text body"))
		}))
		defer server.Close()

		workDir := t.TempDir()
		policy := tool.NewPathPolicy(workDir, config.PathsConfig{})
		env := Env{
			WorkDir:    workDir,
			PathPolicy: &policy,
			httpClient: func() *http.Client { return server.Client() },
		}

		if _, err := NewFetchURLTool(env).Handler(context.Background(), map[string]any{"url": server.URL}); err != nil {
			t.Fatalf("handler: %v", err)
		}
		if ua, _ := getUA.Load().(string); ua != fetchUserAgent {
			t.Errorf("raw text GET User-Agent = %q, want %q", ua, fetchUserAgent)
		}
	})

	t.Run("image GET", func(t *testing.T) {
		imageData, _, _ := newTestPNG()

		var getUA atomic.Value
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			getUA.Store(r.Header.Get("User-Agent"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(imageData)
		}))
		defer server.Close()

		workDir := t.TempDir()
		policy := tool.NewPathPolicy(workDir, config.PathsConfig{})
		env := Env{
			WorkDir:    workDir,
			PathPolicy: &policy,
			httpClient: func() *http.Client { return server.Client() },
		}

		if _, err := NewFetchURLTool(env).Handler(context.Background(), map[string]any{"url": server.URL}); err != nil {
			t.Fatalf("handler: %v", err)
		}
		if ua, _ := getUA.Load().(string); ua != fetchUserAgent {
			t.Errorf("image GET User-Agent = %q, want %q", ua, fetchUserAgent)
		}
	})
}
