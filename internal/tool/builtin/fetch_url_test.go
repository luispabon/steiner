package builtin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

	t.Run("default max_size is 500000", func(t *testing.T) {
		in := &FetchURLInput{URL: "http://example.com"}
		normalizeFetchURL(in)
		if in.MaxSize != 500000 {
			t.Errorf("MaxSize = %d, want 500000", in.MaxSize)
		}
	})

	t.Run("max_size capped at 1000000", func(t *testing.T) {
		in := &FetchURLInput{URL: "http://example.com", MaxSize: 2000000}
		normalizeFetchURL(in)
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

func TestBuildHTMLResult(t *testing.T) {
	t.Run("non-200 status returns structured error using the requested URL", func(t *testing.T) {
		resp := &fetch.Response{
			URL:        "https://example.com/final",
			StatusCode: 404,
		}

		res, err := buildHTMLResult("https://example.com/original", resp, t.TempDir(), 500000)
		if err != nil {
			t.Fatalf("buildHTMLResult: %v", err)
		}
		fetchErr, ok := res.(*FetchURLError)
		if !ok {
			t.Fatalf("expected *FetchURLError, got %T", res)
		}
		if fetchErr.URL != "https://example.com/original" {
			t.Errorf("URL = %q, want the originally requested URL", fetchErr.URL)
		}
		if fetchErr.Error != "HTTP 404" {
			t.Errorf("Error = %q, want %q", fetchErr.Error, "HTTP 404")
		}
	})

	t.Run("content under threshold returns inline result", func(t *testing.T) {
		resp := &fetch.Response{
			URL:        "https://example.com/page",
			StatusCode: 200,
			Markdown:   "# Hello\n\nWorld",
			Metadata: fetch.Metadata{
				Title:       "Hello Page",
				Description: "A test page",
			},
		}

		res, err := buildHTMLResult("https://example.com/page", resp, t.TempDir(), 500000)
		if err != nil {
			t.Fatalf("buildHTMLResult: %v", err)
		}
		result, ok := res.(*FetchURLResult)
		if !ok {
			t.Fatalf("expected *FetchURLResult, got %T", res)
		}
		if result.Content != resp.Markdown {
			t.Errorf("Content = %q, want %q", result.Content, resp.Markdown)
		}
		if result.Title != "Hello Page" {
			t.Errorf("Title = %q, want %q", result.Title, "Hello Page")
		}
		if result.Description != "A test page" {
			t.Errorf("Description = %q, want %q", result.Description, "A test page")
		}
		if result.URL != resp.URL {
			t.Errorf("URL = %q, want %q", result.URL, resp.URL)
		}
		if result.FilePath != "" {
			t.Errorf("FilePath = %q, want empty for inline content", result.FilePath)
		}
		if result.Truncated {
			t.Error("Truncated = true, want false")
		}
	})

	t.Run("content over threshold is saved to disk", func(t *testing.T) {
		markdown := strings.Repeat("a", inlineThreshold+500)
		resp := &fetch.Response{
			URL:        "https://example.com/big",
			StatusCode: 200,
			Markdown:   markdown,
			Metadata: fetch.Metadata{
				Title:       "Big Page",
				Description: "A big test page",
			},
		}

		workDir := t.TempDir()
		res, err := buildHTMLResult("https://example.com/big", resp, workDir, 500000)
		if err != nil {
			t.Fatalf("buildHTMLResult: %v", err)
		}
		result, ok := res.(*FetchURLResult)
		if !ok {
			t.Fatalf("expected *FetchURLResult, got %T", res)
		}
		if result.FilePath == "" {
			t.Error("FilePath is empty, want non-empty for disk-saved content")
		}
		if !strings.HasSuffix(result.FilePath, ".md") {
			t.Errorf("FilePath = %q, want .md extension for HTML-sourced content", result.FilePath)
		}
		if len([]rune(result.Content)) != inlineThreshold {
			t.Errorf("preview length = %d, want %d", len([]rune(result.Content)), inlineThreshold)
		}
		if result.URL != resp.URL {
			t.Errorf("URL = %q, want %q", result.URL, resp.URL)
		}
		if result.Title != "Big Page" {
			t.Errorf("Title = %q, want %q", result.Title, "Big Page")
		}
		if result.Description != "A big test page" {
			t.Errorf("Description = %q, want %q", result.Description, "A big test page")
		}
		if result.StatusCode != 200 {
			t.Errorf("StatusCode = %d, want 200", result.StatusCode)
		}

		data, err := os.ReadFile(filepath.Join(workDir, result.FilePath))
		if err != nil {
			t.Fatalf("read saved file: %v", err)
		}
		if string(data) != markdown {
			t.Errorf("saved file content length = %d, want %d", len(data), len(markdown))
		}
	})

	t.Run("content exceeding max_size is truncated before threshold gating", func(t *testing.T) {
		maxSize := 50
		resp := &fetch.Response{
			URL:        "https://example.com/huge",
			StatusCode: 200,
			Markdown:   strings.Repeat("x", 200),
		}

		res, err := buildHTMLResult("https://example.com/huge", resp, t.TempDir(), maxSize)
		if err != nil {
			t.Fatalf("buildHTMLResult: %v", err)
		}
		result, ok := res.(*FetchURLResult)
		if !ok {
			t.Fatalf("expected *FetchURLResult, got %T", res)
		}
		if !result.Truncated {
			t.Error("Truncated = false, want true")
		}
		if len([]rune(result.Content)) != maxSize {
			t.Errorf("Content length = %d, want %d", len([]rune(result.Content)), maxSize)
		}
	})

	t.Run("save error is propagated when content exceeds threshold", func(t *testing.T) {
		workDir := t.TempDir()
		markdown := strings.Repeat("a", inlineThreshold+500)
		resp := &fetch.Response{
			URL:        "https://example.com/blocked",
			StatusCode: 200,
			Markdown:   markdown,
		}

		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(markdown)))
		fetchedDir := filepath.Join(workDir, ".steiner", "tmp", "fetched")
		if err := os.MkdirAll(filepath.Join(fetchedDir, hash[:12]+".md"), 0o755); err != nil {
			t.Fatalf("setup: %v", err)
		}

		res, err := buildHTMLResult("https://example.com/blocked", resp, workDir, 500000)
		if err == nil {
			t.Fatalf("expected error, got result: %+v", res)
		}
		if res != nil {
			t.Errorf("expected nil result on error, got %+v", res)
		}
	})
}

func TestFetchURLHandlerContentTypeRouting(t *testing.T) {
	// The handler uses toolkit.SafeHTTPClient internally which blocks 127.0.0.1.
	// Handler-level integration testing with httptest.Server is not possible
	// without refactoring the handler to accept an injectable *http.Client.
	// The individual helpers (fetchImageBytes, isImageContentType, etc.) are
	// tested in TestFetchImageBytes and TestContentTypeHelpers.
	// This test validates the routing helpers directly, with emphasis on
	// case-insensitive Content-Type matching (F5 fix).

	t.Run("cleanContentType normalizes case and strips params", func(t *testing.T) {
		tests := []struct {
			input string
			want  string
		}{
			{"image/png", "image/png"},
			{"IMAGE/PNG", "image/png"},
			{"Image/Png", "image/png"},
			{"text/html; charset=utf-8", "text/html"},
			{"TEXT/HTML; CHARSET=UTF-8", "text/html"},
			{" application/octet-stream ", "application/octet-stream"},
			{"", ""},
		}
		for _, tt := range tests {
			got := cleanContentType(tt.input)
			if got != tt.want {
				t.Errorf("cleanContentType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		}
	})

	t.Run("isImageContentType case-insensitive", func(t *testing.T) {
		tests := []struct {
			ct   string
			want bool
		}{
			{"Image/Png", true},
			{"IMAGE/JPEG", true},
			{"image/GIF", true},
			{"IMAGE/WEBP", true},
			{"image/SVG+XML", true},
			{"Text/Html", false},
			{"Application/Pdf", false},
		}
		for _, tt := range tests {
			got := isImageContentType(tt.ct)
			if got != tt.want {
				t.Errorf("isImageContentType(%q) = %v, want %v", tt.ct, got, tt.want)
			}
		}
	})

	t.Run("isTextLikeContentType case-insensitive", func(t *testing.T) {
		tests := []struct {
			ct   string
			want bool
		}{
			{"Text/Html", true},
			{"TEXT/PLAIN", true},
			{"text/Markdown", true},
			{"TEXT/CSV", true},
			{"APPLICATION/JSON", true},
			{"Application/Xhtml+Xml", true},
			{"Application/Javascript", true},
			{"Application/Yaml", true},
			{"Application/Pdf", false},
			{"Image/Png", false},
		}
		for _, tt := range tests {
			got := isTextLikeContentType(tt.ct)
			if got != tt.want {
				t.Errorf("isTextLikeContentType(%q) = %v, want %v", tt.ct, got, tt.want)
			}
		}
	})

	t.Run("raw-text routing excludes HTML and empty content types", func(t *testing.T) {
		// The handler routes to fetchRawText when a content type is text-like,
		// non-HTML, and non-empty.
		takesRawText := func(ct string) bool {
			return isTextLikeContentType(ct) && !isHTMLContentType(ct) && ct != ""
		}
		tests := []struct {
			ct   string
			want bool
		}{
			{"application/json", true},
			{"application/yaml", true},
			{"text/plain", true},
			{"text/csv", true},
			{"application/javascript", true},
			{"text/html", false},
			{"application/xhtml+xml", false},
			{"", false},
			{"application/pdf", false},
			{"image/png", false},
		}
		for _, tt := range tests {
			if got := takesRawText(tt.ct); got != tt.want {
				t.Errorf("takesRawText(%q) = %v, want %v", tt.ct, got, tt.want)
			}
		}
	})

	t.Run("routing decision logic matches handler switch", func(t *testing.T) {
		// Replicate the handler's switch/case decision logic to verify
		// the routing conditions are correct.
		tests := []struct {
			name        string
			contentType string
			url         string
			wantImage   bool // would handler take image path?
			wantText    bool // would handler take wonton/fetch text path?
			wantError   bool // would handler return unsupported error?
		}{
			{
				name:        "image/png",
				contentType: "image/png",
				url:         "http://example.com/f.png",
				wantImage:   true,
				wantText:    false,
				wantError:   false,
			},
			{
				name:        "Image/PNG (mixed case)",
				contentType: "Image/PNG",
				url:         "http://example.com/f.png",
				wantImage:   true,
				wantText:    false,
				wantError:   false,
			},
			{
				name:        "empty content-type, .png url",
				contentType: "",
				url:         "http://example.com/photo.png",
				wantImage:   true,
				wantText:    false,
				wantError:   false,
			},
			{
				name:        "empty content-type, .html url",
				contentType: "",
				url:         "http://example.com/page.html",
				wantImage:   false,
				wantText:    true,
				wantError:   false,
			},
			{
				name:        "octet-stream, .jpg url",
				contentType: "application/octet-stream",
				url:         "http://example.com/photo.jpg",
				wantImage:   true,
				wantText:    false,
				wantError:   false,
			},
			{
				name:        "APPLICATION/OCTET-STREAM (mixed case), .jpg url",
				contentType: "APPLICATION/OCTET-STREAM",
				url:         "http://example.com/photo.jpg",
				wantImage:   true,
				wantText:    false,
				wantError:   false,
			},
			{
				name:        "application/pdf returns error",
				contentType: "application/pdf",
				url:         "http://example.com/doc.pdf",
				wantImage:   false,
				wantText:    false,
				wantError:   true,
			},
			{
				name:        "APPLICATION/PDF (mixed case) returns error",
				contentType: "APPLICATION/PDF",
				url:         "http://example.com/doc.pdf",
				wantImage:   false,
				wantText:    false,
				wantError:   true,
			},
			{
				name:        "text/html goes to wonton",
				contentType: "text/html",
				url:         "http://example.com/page.html",
				wantImage:   false,
				wantText:    true,
				wantError:   false,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				ct := cleanContentType(tt.contentType)

				gotImage := isImageContentType(tt.contentType)
				extFallback := false
				if !gotImage && (ct == "" || ct == "application/octet-stream") {
					extFallback = hasImageExtension(tt.url)
				}

				takesImagePath := gotImage || extFallback
				if takesImagePath && !tt.wantImage {
					t.Errorf("takes image path = true, want false (isImage=%v, extFallback=%v)", gotImage, extFallback)
				}
				if !takesImagePath && tt.wantImage {
					t.Errorf("takes image path = false, want true (isImage=%v, extFallback=%v)", gotImage, extFallback)
				}

				takesTextPath := !gotImage && !extFallback && isTextLikeContentType(tt.contentType)
				if takesTextPath != tt.wantText {
					t.Errorf("takes text path = %v, want %v", takesTextPath, tt.wantText)
				}

				takesErrorPath := !gotImage && !extFallback && !isTextLikeContentType(tt.contentType)
				if takesErrorPath != tt.wantError {
					t.Errorf("takes error path = %v, want %v", takesErrorPath, tt.wantError)
				}
			})
		}
	})
}

func TestFetchRawText(t *testing.T) {
	ctx := context.Background()

	newServer := func(status int, contentType string, body []byte) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			w.WriteHeader(status)
			_, _ = w.Write(body)
		}))
	}

	t.Run("json under threshold returns inline content", func(t *testing.T) {
		body := []byte(`{"hello":"world"}`)
		server := newServer(200, "application/json", body)
		defer server.Close()

		in := FetchURLInput{URL: server.URL, MaxSize: 500000}
		res, err := fetchRawText(ctx, server.Client(), in, t.TempDir(), "application/json")
		if err != nil {
			t.Fatalf("fetchRawText: %v", err)
		}
		result, ok := res.(*FetchURLResult)
		if !ok {
			t.Fatalf("expected *FetchURLResult, got %T", res)
		}
		if result.Content != string(body) {
			t.Errorf("Content = %q, want %q", result.Content, string(body))
		}
		if result.ContentLength != len([]rune(string(body))) {
			t.Errorf("ContentLength = %d, want %d", result.ContentLength, len([]rune(string(body))))
		}
		if result.StatusCode != 200 {
			t.Errorf("StatusCode = %d, want 200", result.StatusCode)
		}
		if result.URL != server.URL {
			t.Errorf("URL = %q, want %q", result.URL, server.URL)
		}
		if result.FilePath != "" {
			t.Errorf("FilePath = %q, want empty for inline content", result.FilePath)
		}
		if result.Truncated {
			t.Error("Truncated = true, want false")
		}
	})

	t.Run("content over threshold is saved to disk", func(t *testing.T) {
		body := []byte(strings.Repeat("a", inlineThreshold+500))
		server := newServer(200, "text/plain", body)
		defer server.Close()

		workDir := t.TempDir()
		in := FetchURLInput{URL: server.URL, MaxSize: 500000}
		res, err := fetchRawText(ctx, server.Client(), in, workDir, "text/plain")
		if err != nil {
			t.Fatalf("fetchRawText: %v", err)
		}
		result, ok := res.(*FetchURLResult)
		if !ok {
			t.Fatalf("expected *FetchURLResult, got %T", res)
		}
		if result.FilePath == "" {
			t.Error("FilePath is empty, want non-empty for disk-saved content")
		}
		if len([]rune(result.Content)) != inlineThreshold {
			t.Errorf("preview length = %d, want %d", len([]rune(result.Content)), inlineThreshold)
		}
		if result.NextOffset == 0 {
			t.Error("NextOffset = 0, want non-zero")
		}
		if result.Message == "" {
			t.Error("Message is empty, want non-empty")
		}
		if result.URL != server.URL {
			t.Errorf("URL = %q, want %q", result.URL, server.URL)
		}
		if result.StatusCode != 200 {
			t.Errorf("StatusCode = %d, want 200", result.StatusCode)
		}

		data, err := os.ReadFile(filepath.Join(workDir, result.FilePath))
		if err != nil {
			t.Fatalf("read saved file: %v", err)
		}
		if string(data) != string(body) {
			t.Errorf("saved file content length = %d, want %d", len(data), len(body))
		}
	})

	t.Run("binary content returns error", func(t *testing.T) {
		body := []byte("hello\x00world")
		server := newServer(200, "text/plain", body)
		defer server.Close()

		in := FetchURLInput{URL: server.URL, MaxSize: 500000}
		res, err := fetchRawText(ctx, server.Client(), in, t.TempDir(), "text/plain")
		if err != nil {
			t.Fatalf("fetchRawText: %v", err)
		}
		fetchErr, ok := res.(*FetchURLError)
		if !ok {
			t.Fatalf("expected *FetchURLError, got %T", res)
		}
		if !strings.Contains(fetchErr.Error, "binary data") {
			t.Errorf("Error = %q, want to contain 'binary data'", fetchErr.Error)
		}
	})

	t.Run("non-200 status returns error", func(t *testing.T) {
		server := newServer(404, "text/plain", []byte("not found"))
		defer server.Close()

		in := FetchURLInput{URL: server.URL, MaxSize: 500000}
		res, err := fetchRawText(ctx, server.Client(), in, t.TempDir(), "text/plain")
		if err != nil {
			t.Fatalf("fetchRawText: %v", err)
		}
		fetchErr, ok := res.(*FetchURLError)
		if !ok {
			t.Fatalf("expected *FetchURLError, got %T", res)
		}
		if fetchErr.Error != "HTTP 404" {
			t.Errorf("Error = %q, want %q", fetchErr.Error, "HTTP 404")
		}
	})

	t.Run("empty body returns inline result with zero length", func(t *testing.T) {
		server := newServer(200, "text/plain", nil)
		defer server.Close()

		in := FetchURLInput{URL: server.URL, MaxSize: 500000}
		res, err := fetchRawText(ctx, server.Client(), in, t.TempDir(), "text/plain")
		if err != nil {
			t.Fatalf("fetchRawText: %v", err)
		}
		result, ok := res.(*FetchURLResult)
		if !ok {
			t.Fatalf("expected *FetchURLResult, got %T", res)
		}
		if result.ContentLength != 0 {
			t.Errorf("ContentLength = %d, want 0", result.ContentLength)
		}
		if result.Content != "" {
			t.Errorf("Content = %q, want empty", result.Content)
		}
	})

	t.Run("content exceeding max_size is truncated", func(t *testing.T) {
		maxSize := 50
		body := []byte(strings.Repeat("x", 200))
		server := newServer(200, "text/plain", body)
		defer server.Close()

		in := FetchURLInput{URL: server.URL, MaxSize: maxSize}
		res, err := fetchRawText(ctx, server.Client(), in, t.TempDir(), "text/plain")
		if err != nil {
			t.Fatalf("fetchRawText: %v", err)
		}
		result, ok := res.(*FetchURLResult)
		if !ok {
			t.Fatalf("expected *FetchURLResult, got %T", res)
		}
		if !result.Truncated {
			t.Error("Truncated = false, want true")
		}
		if len([]rune(result.Content)) != maxSize {
			t.Errorf("Content length = %d, want %d", len([]rune(result.Content)), maxSize)
		}
	})

	t.Run("truncation at a multi-byte UTF-8 boundary does not trigger false binary detection", func(t *testing.T) {
		// Build a JSON body containing multi-byte UTF-8 characters (Japanese
		// text) padded so that a MaxSize cut is likely to land mid-character.
		// We try every maxSize in a small range around a padding boundary to
		// guarantee at least one exercises a genuine mid-rune cut, without
		// relying on knowing UTF-8 byte-boundary arithmetic in the test itself.
		japanese := strings.Repeat("日本語のテキストです。", 50) // multi-byte content
		body := []byte(`{"text":"` + japanese + `"}`)

		for maxSize := len(body) - 20; maxSize < len(body)-1; maxSize++ {
			t.Run(fmt.Sprintf("maxSize=%d", maxSize), func(t *testing.T) {
				server := newServer(200, "application/json", body)
				defer server.Close()

				in := FetchURLInput{URL: server.URL, MaxSize: maxSize}
				res, err := fetchRawText(ctx, server.Client(), in, t.TempDir(), "application/json")
				if err != nil {
					t.Fatalf("fetchRawText: %v", err)
				}
				if fetchErr, ok := res.(*FetchURLError); ok {
					t.Fatalf("got *FetchURLError (false binary-detection positive): %s", fetchErr.Error)
				}
				result, ok := res.(*FetchURLResult)
				if !ok {
					t.Fatalf("expected *FetchURLResult, got %T", res)
				}
				if !result.Truncated {
					t.Error("Truncated = false, want true")
				}
				if !utf8.ValidString(result.Content) {
					t.Error("result.Content is not valid UTF-8")
				}
			})
		}
	})

	t.Run("invalid request URL returns error", func(t *testing.T) {
		in := FetchURLInput{URL: "http://%zz", MaxSize: 500000}
		_, err := fetchRawText(ctx, http.DefaultClient, in, t.TempDir(), "text/plain")
		if err == nil {
			t.Fatal("expected error for invalid request URL, got nil")
		}
	})

	t.Run("transport error returns structured error result", func(t *testing.T) {
		server := newServer(200, "text/plain", []byte("hello"))
		server.Close() // closed before the request so httpClient.Do fails

		in := FetchURLInput{URL: server.URL, MaxSize: 500000}
		res, err := fetchRawText(ctx, server.Client(), in, t.TempDir(), "text/plain")
		if err != nil {
			t.Fatalf("fetchRawText: %v", err)
		}
		fetchErr, ok := res.(*FetchURLError)
		if !ok {
			t.Fatalf("expected *FetchURLError, got %T", res)
		}
		if fetchErr.Error == "" {
			t.Error("Error is empty, want a transport error message")
		}
	})

	t.Run("body read error returns structured error result", func(t *testing.T) {
		client := &http.Client{Transport: brokenBodyRoundTripper{}}
		in := FetchURLInput{URL: "http://broken-body.invalid", MaxSize: 500000}
		res, err := fetchRawText(ctx, client, in, t.TempDir(), "text/plain")
		if err != nil {
			t.Fatalf("fetchRawText: %v", err)
		}
		fetchErr, ok := res.(*FetchURLError)
		if !ok {
			t.Fatalf("expected *FetchURLError, got %T", res)
		}
		if !strings.Contains(fetchErr.Error, "broken body") {
			t.Errorf("Error = %q, want to contain 'broken body'", fetchErr.Error)
		}
	})

	t.Run("save error is propagated when content exceeds threshold", func(t *testing.T) {
		workDir := t.TempDir()
		body := []byte(strings.Repeat("a", inlineThreshold+500))
		server := newServer(200, "text/plain", body)
		defer server.Close()

		// Pre-create the target file path as a directory so saveFetchedContent's
		// os.WriteFile fails with "is a directory".
		hash := fmt.Sprintf("%x", sha256.Sum256(body))
		fetchedDir := filepath.Join(workDir, ".steiner", "tmp", "fetched")
		if err := os.MkdirAll(filepath.Join(fetchedDir, hash[:12]+".txt"), 0o755); err != nil {
			t.Fatalf("setup: %v", err)
		}

		in := FetchURLInput{URL: server.URL, MaxSize: 500000}
		res, err := fetchRawText(ctx, server.Client(), in, workDir, "text/plain")
		if err == nil {
			t.Fatalf("expected error, got result: %+v", res)
		}
		if res != nil {
			t.Errorf("expected nil result on error, got %+v", res)
		}
	})
}

// brokenBodyRoundTripper returns a 200 response whose body errors on Read,
// to exercise io.ReadAll failure handling.
type brokenBodyRoundTripper struct{}

func (brokenBodyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(brokenReader{}),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) {
	return 0, fmt.Errorf("broken body: simulated read failure")
}

// newTestPNG returns a PNG-encoded 2x2 test image as bytes and the expected dimensions.
func newTestPNG() ([]byte, int, int) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	img.Set(1, 0, color.RGBA{0, 255, 0, 255})
	img.Set(0, 1, color.RGBA{0, 0, 255, 255})
	img.Set(1, 1, color.RGBA{255, 255, 255, 255})
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes(), 2, 2
}

// newTestWebP returns a small lossless WebP image with known dimensions.
func newTestWebP() ([]byte, int, int) {
	return []byte{
		0x52, 0x49, 0x46, 0x46, 0x1c, 0x00, 0x00, 0x00,
		0x57, 0x45, 0x42, 0x50, 0x56, 0x50, 0x38, 0x4c,
		0x0f, 0x00, 0x00, 0x00, 0x2f, 0x01, 0x80, 0x00,
		0x00, 0x07, 0x10, 0xfd, 0x8f, 0xfe, 0x07, 0x22,
		0xa2, 0xff, 0x01, 0x00,
	}, 2, 3
}

func TestFetchImageBytes(t *testing.T) {
	ctx := context.Background()
	httpClient := &http.Client{Timeout: 5 * time.Second}

	pngData, pngWidth, pngHeight := newTestPNG()

	t.Run("fetches raw image bytes and metadata", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngData)
		}))
		defer server.Close()

		fetched, statusCode, err := fetchImageBytes(ctx, httpClient, server.URL, "image/png")
		if err != nil {
			t.Fatalf("fetchImageBytes returned error: %v", err)
		}
		if fetched == nil {
			t.Fatal("fetched image is nil")
		}
		if !bytes.Equal(fetched.data, pngData) {
			t.Error("raw image data does not match original")
		}
		if fetched.mediaType != "image/png" {
			t.Errorf("mediaType = %q, want %q", fetched.mediaType, "image/png")
		}
		if fetched.extension != ".png" {
			t.Errorf("extension = %q, want %q", fetched.extension, ".png")
		}
		if fetched.width != pngWidth || fetched.height != pngHeight {
			t.Errorf("dimensions = %dx%d, want %dx%d", fetched.width, fetched.height, pngWidth, pngHeight)
		}
		if statusCode != 200 {
			t.Errorf("StatusCode = %d, want 200", statusCode)
		}

	})
	t.Run("fetches valid WebP bytes and dimensions", func(t *testing.T) {
		webpData, webpWidth, webpHeight := newTestWebP()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/webp")
			_, _ = w.Write(webpData)
		}))
		defer server.Close()

		fetched, statusCode, err := fetchImageBytes(ctx, httpClient, server.URL, "image/webp")
		if err != nil {
			t.Fatalf("fetchImageBytes returned error: %v", err)
		}
		if fetched == nil {
			t.Fatal("fetched image is nil")
		}
		if !bytes.Equal(fetched.data, webpData) {
			t.Error("raw WebP data does not match original")
		}
		if fetched.mediaType != "image/webp" || fetched.extension != ".webp" {
			t.Errorf("metadata = (%q, %q), want (image/webp, .webp)", fetched.mediaType, fetched.extension)
		}
		if fetched.width != webpWidth || fetched.height != webpHeight {
			t.Errorf("dimensions = %dx%d, want %dx%d", fetched.width, fetched.height, webpWidth, webpHeight)
		}
		if statusCode != http.StatusOK {
			t.Errorf("StatusCode = %d, want %d", statusCode, http.StatusOK)
		}
	})

	t.Run("oversize image returns error before saving", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(make([]byte, maxImageBytes+1))
		}))
		defer server.Close()

		workDir := t.TempDir()
		_, _, err := fetchImageBytes(ctx, httpClient, server.URL, "image/png")
		if err == nil {
			t.Fatal("expected error for oversize image, got nil")
		}
		if !strings.Contains(err.Error(), "too large") {
			t.Errorf("Error = %q, want to contain 'too large'", err.Error())
		}
		if _, err := os.Stat(filepath.Join(workDir, ".steiner", "tmp", "fetched")); !os.IsNotExist(err) {
			t.Errorf("fetched directory exists after failed fetch, stat err = %v", err)
		}
	})

	t.Run("invalid image returns error before saving", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("not an image"))
		}))
		defer server.Close()

		workDir := t.TempDir()
		_, _, err := fetchImageBytes(ctx, httpClient, server.URL, "image/png")
		if err == nil {
			t.Fatal("expected invalid image error, got nil")
		}
		if !strings.Contains(err.Error(), "invalid image") {
			t.Errorf("Error = %q, want to contain 'invalid image'", err.Error())
		}
		if _, err := os.Stat(filepath.Join(workDir, ".steiner", "tmp", "fetched")); !os.IsNotExist(err) {
			t.Errorf("fetched directory exists after failed fetch, stat err = %v", err)
		}
	})

	t.Run("media type from Content-Type header", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(pngData)
		}))
		defer server.Close()

		fetched, _, err := fetchImageBytes(ctx, httpClient, server.URL, "image/jpeg")
		if err != nil {
			t.Fatalf("fetchImageBytes returned error: %v", err)
		}
		if fetched.mediaType != "image/jpeg" || fetched.extension != ".jpg" {
			t.Errorf("metadata = (%q, %q), want (image/jpeg, .jpg)", fetched.mediaType, fetched.extension)
		}
	})

	t.Run("media type and extension fallback", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(pngData)
		}))
		defer server.Close()

		fetched, _, err := fetchImageBytes(ctx, httpClient, server.URL+"/photo.png", "application/octet-stream")
		if err != nil {
			t.Fatalf("fetchImageBytes returned error: %v", err)
		}
		if fetched.mediaType != "image/png" || fetched.extension != ".png" {
			t.Errorf("metadata = (%q, %q), want (image/png, .png)", fetched.mediaType, fetched.extension)
		}
	})
}

func TestIsHTMLContentType(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"text/html", true},
		{"application/xhtml+xml", true},
		{"TEXT/HTML", true},
		{"text/html; charset=utf-8", true},
		{"", false},
		{"application/json", false},
		{"text/plain", false},
	}
	for _, tt := range tests {
		got := isHTMLContentType(tt.ct)
		if got != tt.want {
			t.Errorf("isHTMLContentType(%q) = %v, want %v", tt.ct, got, tt.want)
		}
	}
}

func TestIsBinaryContent(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"nil data", nil, false},
		{"empty data", []byte{}, false},
		{"plain ASCII text", []byte("hello world"), false},
		{"valid UTF-8 multibyte", []byte("héllo wörld 日本語"), false},
		{"contains null byte", []byte("hello\x00world"), true},
		{"invalid UTF-8", []byte{0xff, 0xfe, 0xfd}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isBinaryContent(tt.data)
			if got != tt.want {
				t.Errorf("isBinaryContent(%v) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}

func TestExtensionFromContentType(t *testing.T) {
	tests := []struct {
		ct   string
		want string
	}{
		{"application/json", ".json"},
		{"application/yaml", ".yaml"},
		{"application/x-yaml", ".yaml"},
		{"text/plain", ".txt"},
		{"text/csv", ".csv"},
		{"text/markdown", ".md"},
		{"application/xml", ".xml"},
		{"text/xml", ".xml"},
		{"application/javascript", ".js"},
		{"application/typescript", ".ts"},
		{"text/typescript", ".ts"},
		{"text/html", ".md"},
		{"application/xhtml+xml", ".md"},
		{"application/octet-stream", ".txt"},
		{"", ".txt"},
		{"application/json; charset=utf-8", ".json"},
	}
	for _, tt := range tests {
		got := extensionFromContentType(tt.ct)
		if got != tt.want {
			t.Errorf("extensionFromContentType(%q) = %q, want %q", tt.ct, got, tt.want)
		}
	}
}

func TestContentTypeHelpers(t *testing.T) {
	t.Run("isImageContentType", func(t *testing.T) {
		tests := []struct {
			ct   string
			want bool
		}{
			{"image/png", true},
			{"image/jpeg", true},
			{"image/gif", true},
			{"image/webp", true},
			{"image/svg+xml", true},
			{"text/html", false},
			{"application/pdf", false},
			{"", false},
			{"image/png; charset=utf-8", true},
		}
		for _, tt := range tests {
			got := isImageContentType(tt.ct)
			if got != tt.want {
				t.Errorf("isImageContentType(%q) = %v, want %v", tt.ct, got, tt.want)
			}
		}
	})

	t.Run("isTextLikeContentType", func(t *testing.T) {
		tests := []struct {
			ct   string
			want bool
		}{
			// Existing text/* and application types
			{"text/html", true},
			{"text/plain", true},
			{"text/xml", true},
			{"application/xhtml+xml", true},
			{"application/json", true},
			{"", true}, // missing Content-Type treated as text-like
			{"text/html; charset=utf-8", true},
			// New text/* prefix matches
			{"text/csv", true},
			{"text/calendar", true},
			// New explicit application types
			{"application/javascript", true},
			{"application/typescript", true},
			{"application/yaml", true},
			{"application/x-yaml", true},
			{"application/ld+json", true},
			{"application/graphql", true},
			{"application/x-www-form-urlencoded", true},
			// Binary types that should not match
			{"application/pdf", false},
			{"image/png", false},
			{"video/mp4", false},
		}
		for _, tt := range tests {
			got := isTextLikeContentType(tt.ct)
			if got != tt.want {
				t.Errorf("isTextLikeContentType(%q) = %v, want %v", tt.ct, got, tt.want)
			}
		}
	})

	t.Run("hasImageExtension", func(t *testing.T) {
		tests := []struct {
			url  string
			want bool
		}{
			{"https://example.com/photo.png", true},
			{"https://example.com/photo.jpg", true},
			{"https://example.com/photo.jpeg", true},
			{"https://example.com/photo.gif", true},
			{"https://example.com/photo.webp", true},
			{"https://example.com/photo.PNG", true},
			{"https://example.com/page.html", false},
			{"https://example.com/noext", false},
			{"https://example.com/image.png?raw=1", true},
		}
		for _, tt := range tests {
			got := hasImageExtension(tt.url)
			if got != tt.want {
				t.Errorf("hasImageExtension(%q) = %v, want %v", tt.url, got, tt.want)
			}
		}
	})

	t.Run("mediaTypeFromResponse", func(t *testing.T) {
		tests := []struct {
			contentType string
			url         string
			want        string
		}{
			{"image/png", "https://example.com/a.png", "image/png"},
			{"image/jpeg", "https://example.com/a.jpg", "image/jpeg"},
			{"", "https://example.com/photo.png", "image/png"},
			{"", "https://example.com/photo.jpg", "image/jpeg"},
			{"application/octet-stream", "https://example.com/photo.gif", "image/gif"},
			{"", "https://example.com/unknown.xyz", "image/unknown"},
			{"image/svg+xml", "https://example.com/a.svg", "image/svg+xml"},
		}
		for _, tt := range tests {
			got := mediaTypeFromResponse(tt.contentType, tt.url)
			if got != tt.want {
				t.Errorf("mediaTypeFromResponse(%q, %q) = %q, want %q", tt.contentType, tt.url, got, tt.want)
			}
		}
	})
}

func TestSaveFetchedContent(t *testing.T) {
	t.Run("creates file at expected path with matching content", func(t *testing.T) {
		workDir := t.TempDir()
		content := "hello world"

		result, err := saveFetchedContent(workDir, content, "text/plain", false)
		if err != nil {
			t.Fatalf("saveFetchedContent: %v", err)
		}

		if !strings.HasPrefix(result.FilePath, filepath.Join(".steiner", "tmp", "fetched")+string(filepath.Separator)) {
			t.Fatalf("FilePath = %q, want prefix %q", result.FilePath, filepath.Join(".steiner", "tmp", "fetched"))
		}
		if filepath.IsAbs(result.FilePath) {
			t.Errorf("FilePath = %q, want relative path", result.FilePath)
		}

		data, err := os.ReadFile(filepath.Join(workDir, result.FilePath))
		if err != nil {
			t.Fatalf("read saved file: %v", err)
		}
		if string(data) != content {
			t.Errorf("saved content = %q, want %q", string(data), content)
		}
	})

	t.Run("preview capped at inlineThreshold for long content", func(t *testing.T) {
		workDir := t.TempDir()
		content := strings.Repeat("a", inlineThreshold+500)

		result, err := saveFetchedContent(workDir, content, "text/plain", false)
		if err != nil {
			t.Fatalf("saveFetchedContent: %v", err)
		}

		if len([]rune(result.Content)) != inlineThreshold {
			t.Errorf("preview length = %d, want %d", len([]rune(result.Content)), inlineThreshold)
		}
	})

	t.Run("preview equals full content when shorter than threshold", func(t *testing.T) {
		workDir := t.TempDir()
		content := "short content"

		result, err := saveFetchedContent(workDir, content, "text/plain", false)
		if err != nil {
			t.Fatalf("saveFetchedContent: %v", err)
		}

		if result.Content != content {
			t.Errorf("Content = %q, want %q", result.Content, content)
		}
	})

	t.Run("next offset matches newline count in preview plus one", func(t *testing.T) {
		workDir := t.TempDir()
		var sb strings.Builder
		for sb.Len() < inlineThreshold+1000 {
			sb.WriteString("line of text\n")
		}
		content := sb.String()

		result, err := saveFetchedContent(workDir, content, "text/plain", false)
		if err != nil {
			t.Fatalf("saveFetchedContent: %v", err)
		}

		preview := string([]rune(content)[:inlineThreshold])
		want := strings.Count(preview, "\n") + 1
		if result.NextOffset != want {
			t.Errorf("NextOffset = %d, want %d", result.NextOffset, want)
		}
	})

	t.Run("truncated and non-truncated messages differ", func(t *testing.T) {
		workDir := t.TempDir()
		content := "some content"

		truncatedResult, err := saveFetchedContent(workDir, content, "text/plain", true)
		if err != nil {
			t.Fatalf("saveFetchedContent: %v", err)
		}
		if !strings.Contains(truncatedResult.Message, "truncated") || !strings.Contains(truncatedResult.Message, "saved portion") {
			t.Errorf("truncated Message = %q, want mentions of truncation and saved portion", truncatedResult.Message)
		}

		normalResult, err := saveFetchedContent(workDir, content, "text/plain", false)
		if err != nil {
			t.Fatalf("saveFetchedContent: %v", err)
		}

		if truncatedResult.Message == normalResult.Message {
			t.Errorf("expected differing messages, got same: %q", truncatedResult.Message)
		}
	})

	t.Run("creates directory if missing", func(t *testing.T) {
		workDir := t.TempDir()
		fetchedDir := filepath.Join(workDir, ".steiner", "tmp", "fetched")
		if _, err := os.Stat(fetchedDir); !os.IsNotExist(err) {
			t.Fatalf("expected fetched dir to not exist yet, stat err = %v", err)
		}

		if _, err := saveFetchedContent(workDir, "content", "text/plain", false); err != nil {
			t.Fatalf("saveFetchedContent: %v", err)
		}

		if _, err := os.Stat(fetchedDir); err != nil {
			t.Errorf("expected fetched dir to exist, stat err = %v", err)
		}
	})

	t.Run("identical content and content type produce same file path", func(t *testing.T) {
		workDir := t.TempDir()
		content := "identical content"

		first, err := saveFetchedContent(workDir, content, "text/plain", false)
		if err != nil {
			t.Fatalf("saveFetchedContent: %v", err)
		}
		second, err := saveFetchedContent(workDir, content, "text/plain", false)
		if err != nil {
			t.Fatalf("saveFetchedContent: %v", err)
		}

		if first.FilePath != second.FilePath {
			t.Errorf("FilePath mismatch: %q vs %q", first.FilePath, second.FilePath)
		}
	})

	t.Run("content length reflects full content not preview", func(t *testing.T) {
		workDir := t.TempDir()
		content := strings.Repeat("b", inlineThreshold+250)

		result, err := saveFetchedContent(workDir, content, "text/plain", false)
		if err != nil {
			t.Fatalf("saveFetchedContent: %v", err)
		}

		if result.ContentLength != inlineThreshold+250 {
			t.Errorf("ContentLength = %d, want %d", result.ContentLength, inlineThreshold+250)
		}
	})

	t.Run("mkdir failure returns wrapped error", func(t *testing.T) {
		workDir := t.TempDir()
		// Create a regular file where saveFetchedContent needs to create a
		// directory (.steiner/tmp/fetched), so os.MkdirAll fails.
		blocked := filepath.Join(workDir, ".steiner", "tmp")
		if err := os.MkdirAll(filepath.Dir(blocked), 0o755); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(blocked, []byte("not a directory"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, err := saveFetchedContent(workDir, "content", "text/plain", false)
		if err == nil {
			t.Fatal("expected error when directory creation is blocked, got nil")
		}
		if !strings.Contains(err.Error(), "save fetched content") {
			t.Errorf("error = %q, want to contain %q", err.Error(), "save fetched content")
		}
	})

	t.Run("write failure returns wrapped error", func(t *testing.T) {
		workDir := t.TempDir()
		content := "content"
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
		ext := extensionFromContentType("text/plain")

		// Pre-create the target file path as a directory so os.WriteFile fails.
		targetAsDir := filepath.Join(workDir, ".steiner", "tmp", "fetched", hash[:12]+ext)
		if err := os.MkdirAll(targetAsDir, 0o755); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, err := saveFetchedContent(workDir, content, "text/plain", false)
		if err == nil {
			t.Fatal("expected error when write target is a directory, got nil")
		}
		if !strings.Contains(err.Error(), "save fetched content") {
			t.Errorf("error = %q, want to contain %q", err.Error(), "save fetched content")
		}
	})
}

func TestSaveFetchedImage(t *testing.T) {
	workDir := t.TempDir()
	data, width, height := newTestPNG()
	image := &fetchedImage{
		data:      data,
		mediaType: "image/png",
		extension: ".png",
		width:     width,
		height:    height,
	}

	result, err := saveFetchedImage(workDir, image)
	if err != nil {
		t.Fatalf("saveFetchedImage: %v", err)
	}
	if result.Image != nil {
		t.Fatal("Image is non-nil, want deferred image persistence")
	}
	if filepath.IsAbs(result.FilePath) {
		t.Fatalf("FilePath = %q, want relative path", result.FilePath)
	}
	if !strings.HasPrefix(result.FilePath, filepath.Join(".steiner", "tmp", "fetched")+string(filepath.Separator)) {
		t.Fatalf("FilePath = %q, want fetched directory", result.FilePath)
	}
	if !strings.Contains(result.Message, "read") {
		t.Errorf("Message = %q, want read guidance", result.Message)
	}

	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	wantName := hash[:12] + ".png"
	if filepath.Base(result.FilePath) != wantName {
		t.Errorf("file name = %q, want %q", filepath.Base(result.FilePath), wantName)
	}
	saved, err := os.ReadFile(filepath.Join(workDir, result.FilePath))
	if err != nil {
		t.Fatalf("read saved image: %v", err)
	}
	if !bytes.Equal(saved, data) {
		t.Error("saved image bytes do not match source bytes")
	}
	info, err := os.Stat(filepath.Join(workDir, result.FilePath))
	if err != nil {
		t.Fatalf("stat saved image: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("saved image mode = %o, want 0o600", info.Mode().Perm())
	}
}

func TestSaveFetchedImageWriteFailureLeavesNoPartialFile(t *testing.T) {
	workDir := t.TempDir()
	data, _, _ := newTestPNG()
	image := &fetchedImage{data: data, extension: ".png"}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	target := filepath.Join(workDir, ".steiner", "tmp", "fetched", hash[:12]+".png")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := saveFetchedImage(workDir, image); err == nil {
		t.Fatal("expected save error when target is a directory")
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatalf("read fetched directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(target) {
		t.Errorf("fetched directory entries = %v, want only failed target directory", entries)
	}
}
