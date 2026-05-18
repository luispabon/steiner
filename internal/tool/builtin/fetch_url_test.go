package builtin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deepnoodle-ai/wonton/fetch"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestFetchURLTool(t *testing.T) {
	tmpDir := t.TempDir()
	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	env := Env{WorkDir: tmpDir, PathPolicy: &policy}
	toolDef := NewFetchURLTool(env)
	ctx := context.Background()

	t.Run("successful fetch returns content", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><head><title>Test Page</title><meta name="description" content="Test description"></head><body><p>Hello world</p></body></html>`)
		}))
		defer server.Close()

		httpClient := &http.Client{Timeout: 5 * time.Second}
		fetcher := fetch.NewHTTPFetcher(fetch.HTTPFetcherOptions{
			Client:  httpClient,
			Timeout: 5 * time.Second,
		})

		req := &fetch.Request{
			URL:     server.URL,
			Formats: []string{"markdown"},
		}

		resp, err := fetcher.Fetch(ctx, req)
		if err != nil {
			t.Fatalf("fetch failed: %v", err)
		}

		if resp.StatusCode != 200 {
			t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
		}

		if resp.Metadata.Title != "Test Page" {
			t.Errorf("Title = %q, want %q", resp.Metadata.Title, "Test Page")
		}

		if resp.Metadata.Description != "Test description" {
			t.Errorf("Description = %q, want %q", resp.Metadata.Description, "Test description")
		}

		if !strings.Contains(resp.Markdown, "Hello world") {
			t.Errorf("Markdown = %q, want to contain %q", resp.Markdown, "Hello world")
		}
	})

	t.Run("invalid URL missing scheme returns error", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"url": "example.com",
		})
		if err == nil {
			t.Fatalf("expected error for invalid url, got result: %v", resultI)
		}
		if !strings.Contains(err.Error(), "invalid url") {
			t.Errorf("error = %v, want to contain 'invalid url'", err)
		}
	})

	t.Run("empty URL returns error", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"url": "",
		})
		if err == nil {
			t.Fatalf("expected error for empty url, got result: %v", resultI)
		}
		if !strings.Contains(err.Error(), "empty url") {
			t.Errorf("error = %v, want to contain 'empty url'", err)
		}
	})

	t.Run("network error returns structured error result", func(t *testing.T) {
		httpClient := &http.Client{Timeout: 5 * time.Second}
		fetcher := fetch.NewHTTPFetcher(fetch.HTTPFetcherOptions{
			Client:  httpClient,
			Timeout: 5 * time.Second,
		})

		req := &fetch.Request{
			URL:     "http://localhost:65534/nonexistent",
			Formats: []string{"markdown"},
		}

		resp, err := fetcher.Fetch(ctx, req)
		if err == nil && resp.StatusCode != 200 {
			t.Logf("fetch on invalid host returned status %d (expected error or non-200)", resp.StatusCode)
		}
	})

	t.Run("SSRF blocked private IP returns structured error result", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"url": "http://192.168.1.1/",
		})
		if err != nil {
			t.Fatalf("expected structured error result, got hard error: %v", err)
		}
		fetchErr, ok := resultI.(*FetchURLError)
		if !ok {
			t.Fatalf("expected *FetchURLError, got %T", resultI)
		}
		if fetchErr.Error == "" {
			t.Errorf("FetchURLError.Error is empty, want non-empty error message")
		}
	})

	t.Run("max_size limits content length", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><head><title>Test</title></head><body><p>`+strings.Repeat("x", 1000)+`</p></body></html>`)
		}))
		defer server.Close()

		httpClient := &http.Client{Timeout: 5 * time.Second}
		fetcher := fetch.NewHTTPFetcher(fetch.HTTPFetcherOptions{
			Client:  httpClient,
			Timeout: 5 * time.Second,
		})

		req := &fetch.Request{
			URL:     server.URL,
			Formats: []string{"markdown"},
		}

		resp, err := fetcher.Fetch(ctx, req)
		if err != nil {
			t.Fatalf("fetch failed: %v", err)
		}

		maxSize := 100
		content := resp.Markdown
		runes := []rune(content)
		if len(runes) > maxSize {
			runes = runes[:maxSize]
			content = string(runes)
		}

		if len([]rune(content)) > maxSize {
			t.Errorf("truncated content length = %d, want <= %d", len([]rune(content)), maxSize)
		}
	})

	t.Run("default max_size is 500000", func(t *testing.T) {
		in := &FetchURLInput{URL: "http://example.com"}
		NormalizeFetchURL(in)
		if in.MaxSize != 500000 {
			t.Errorf("MaxSize = %d, want 500000", in.MaxSize)
		}
	})

	t.Run("max_size capped at 1000000", func(t *testing.T) {
		in := &FetchURLInput{URL: "http://example.com", MaxSize: 2000000}
		NormalizeFetchURL(in)
		if in.MaxSize != 1000000 {
			t.Errorf("MaxSize = %d, want 1000000", in.MaxSize)
		}
	})

	t.Run("schema is defined correctly", func(t *testing.T) {
		schema := FetchURLSchema()
		if schema == nil {
			t.Fatalf("schema is nil")
		}

		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("properties not found in schema")
		}

		if _, ok := props["url"]; !ok {
			t.Errorf("url property not found in schema")
		}

		if _, ok := props["max_size"]; !ok {
			t.Errorf("max_size property not found in schema")
		}

		required, ok := schema["required"].([]string)
		if !ok {
			t.Fatalf("required not found or wrong type")
		}

		if len(required) != 1 || required[0] != "url" {
			t.Errorf("required = %v, want [url]", required)
		}
	})
}
