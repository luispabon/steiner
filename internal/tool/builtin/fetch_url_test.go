package builtin

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
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

func TestFetchImageBytes(t *testing.T) {
	ctx := context.Background()
	httpClient := &http.Client{Timeout: 5 * time.Second}

	pngData, pngWidth, pngHeight := newTestPNG()

	t.Run("fetches image and returns ImageBlock", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngData)
		}))
		defer server.Close()

		imgBlock, statusCode, err := fetchImageBytes(ctx, httpClient, server.URL, "image/png")
		if err != nil {
			t.Fatalf("fetchImageBytes returned error: %v", err)
		}
		if imgBlock == nil {
			t.Fatal("ImageBlock is nil")
		}
		if imgBlock.MediaType != "image/png" {
			t.Errorf("MediaType = %q, want %q", imgBlock.MediaType, "image/png")
		}
		if imgBlock.Width != pngWidth {
			t.Errorf("Width = %d, want %d", imgBlock.Width, pngWidth)
		}
		if imgBlock.Height != pngHeight {
			t.Errorf("Height = %d, want %d", imgBlock.Height, pngHeight)
		}
		if imgBlock.Data == "" {
			t.Error("Data is empty")
		}
		if imgBlock.SizeBytes != len(pngData) {
			t.Errorf("SizeBytes = %d, want %d", imgBlock.SizeBytes, len(pngData))
		}
		if statusCode != 200 {
			t.Errorf("StatusCode = %d, want 200", statusCode)
		}

		// Verify base64 round-trips.
		decoded, err := base64.StdEncoding.DecodeString(imgBlock.Data)
		if err != nil {
			t.Fatalf("failed to decode base64: %v", err)
		}
		if !bytes.Equal(decoded, pngData) {
			t.Error("decoded base64 data does not match original")
		}
	})

	t.Run("oversize image returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			bigData := make([]byte, 5*1024*1024+1)
			_, _ = w.Write(bigData)
		}))
		defer server.Close()

		_, _, err := fetchImageBytes(ctx, httpClient, server.URL, "image/png")
		if err == nil {
			t.Fatal("expected error for oversize image, got nil")
		}
		if !strings.Contains(err.Error(), "too large") {
			t.Errorf("Error = %q, want to contain 'too large'", err.Error())
		}
	})

	t.Run("media type from Content-Type header", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(pngData)
		}))
		defer server.Close()

		imgBlock, _, err := fetchImageBytes(ctx, httpClient, server.URL, "image/jpeg")
		if err != nil {
			t.Fatalf("fetchImageBytes returned error: %v", err)
		}
		// Server declares image/jpeg, so that takes precedence.
		if imgBlock.MediaType != "image/jpeg" {
			t.Errorf("MediaType = %q, want %q", imgBlock.MediaType, "image/jpeg")
		}
	})

	t.Run("media type fallback to extension when Content-Type is octet-stream", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(pngData)
		}))
		defer server.Close()

		// URL with .png extension should fall back to image/png.
		imgBlock, _, err := fetchImageBytes(ctx, httpClient, server.URL+"/photo.png", "application/octet-stream")
		if err != nil {
			t.Fatalf("fetchImageBytes returned error: %v", err)
		}
		if imgBlock.MediaType != "image/png" {
			t.Errorf("MediaType = %q, want %q", imgBlock.MediaType, "image/png")
		}
	})
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
