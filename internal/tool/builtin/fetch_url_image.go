package builtin

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"  // register GIF decoder for image.Decode
	_ "image/jpeg" // register JPEG decoder for image.Decode
	_ "image/png"  // register PNG decoder for image.Decode
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

const maxImageBytes = 5 * 1024 * 1024

// fetchImageBytes fetches an image from urlStr, validates size (max 5MB),
// base64-encodes, decodes dimensions, and returns an ImageBlock plus the
// HTTP status code. mediaTypeHint is the response Content-Type; when empty
// or application/octet-stream, the URL extension is used as fallback.
func fetchImageBytes(ctx context.Context, httpClient *http.Client, urlStr, mediaTypeHint string) (*ImageBlock, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch image: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch image: %w", err)
	}
	defer resp.Body.Close()

	// Read up to maxImageBytes + 1 to detect oversize.
	limited := io.LimitReader(resp.Body, maxImageBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch image: %w", err)
	}

	if len(data) > maxImageBytes {
		return nil, resp.StatusCode, fmt.Errorf("fetch image: too large (max %s, got %s)",
			output.FormatFileSize(maxImageBytes), output.FormatFileSize(len(data)))
	}

	// Determine media type.
	mediaType := mediaTypeFromResponse(mediaTypeHint, urlStr)

	// Decode dimensions.
	width, height := 0, 0
	img, _, err := image.Decode(bytes.NewReader(data))
	if err == nil {
		bounds := img.Bounds()
		width = bounds.Max.X
		height = bounds.Max.Y
	}

	encoded := base64.StdEncoding.EncodeToString(data)

	return &ImageBlock{
		MediaType: mediaType,
		Data:      encoded,
		Width:     width,
		Height:    height,
		SizeBytes: len(data),
	}, resp.StatusCode, nil
}

// mediaTypeFromResponse returns a media type from the Content-Type header,
// falling back to URL extension when the header is empty or octet-stream.
func mediaTypeFromResponse(contentType, urlStr string) string {
	ct := cleanContentType(contentType)

	if ct != "" && ct != "application/octet-stream" {
		return ct
	}

	// Fall back to extension.
	ext := strings.ToLower(filepath.Ext(urlPathNoQuery(urlStr)))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "image/unknown"
	}
}

// cleanContentType strips parameters from a Content-Type value.
func cleanContentType(contentType string) string {
	if idx := strings.Index(contentType, ";"); idx >= 0 {
		contentType = strings.TrimSpace(contentType[:idx])
	}
	return strings.TrimSpace(contentType)
}

// isImageContentType reports whether contentType indicates an image.
func isImageContentType(contentType string) bool {
	return strings.HasPrefix(cleanContentType(contentType), "image/")
}

// isTextLikeContentType reports whether contentType is safe to pass to
// wonton/fetch for HTML-to-markdown conversion.
func isTextLikeContentType(contentType string) bool {
	ct := cleanContentType(contentType)
	if ct == "" {
		return true
	}
	switch ct {
	case "text/html", "text/plain", "text/markdown", "text/xml",
		"application/xhtml+xml", "application/xml", "application/json":
		return true
	default:
		return false
	}
}

// urlPathNoQuery returns the path component of a URL without the query string.
func urlPathNoQuery(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Path
}

// hasImageExtension reports whether the URL path has an image file extension.
func hasImageExtension(rawURL string) bool {
	path := urlPathNoQuery(rawURL)
	return IsImageExtension(filepath.Ext(path))
}
