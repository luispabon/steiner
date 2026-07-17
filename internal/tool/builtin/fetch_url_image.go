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
	"unicode/utf8"

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
	defer resp.Body.Close() //nolint:errcheck

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
	return strings.ToLower(strings.TrimSpace(contentType))
}

// isImageContentType reports whether contentType indicates an image.
func isImageContentType(contentType string) bool {
	return strings.HasPrefix(cleanContentType(contentType), "image/")
}

// isTextLikeContentType reports whether contentType is safe to pass to
// wonton/fetch for HTML-to-markdown conversion. It accepts any text/* type
// plus common application text types (javascript, yaml, json, etc).
func isTextLikeContentType(contentType string) bool {
	ct := cleanContentType(contentType)
	if ct == "" {
		return true
	}

	// Accept all text/* types.
	if strings.HasPrefix(ct, "text/") {
		return true
	}

	// Accept explicit application text types.
	switch ct {
	case "application/javascript", "application/typescript",
		"application/yaml", "application/x-yaml",
		"application/ld+json", "application/graphql",
		"application/x-www-form-urlencoded",
		"application/xhtml+xml", "application/xml", "application/json":
		return true
	default:
		return false
	}
}

// isHTMLContentType reports whether contentType indicates HTML content.
func isHTMLContentType(contentType string) bool {
	switch cleanContentType(contentType) {
	case "text/html", "application/xhtml+xml":
		return true
	default:
		return false
	}
}

// isBinaryContent reports whether data looks like binary content, based on
// the presence of a null byte or invalid UTF-8.
func isBinaryContent(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)
}

// trimIncompleteUTF8Suffix removes a trailing incomplete UTF-8 rune from
// data, if present. Truncating a byte slice at an arbitrary byte boundary
// can cut a multi-byte UTF-8 character in half; this recovers the largest
// valid-UTF-8 prefix by trimming at most utf8.UTFMax-1 bytes off the end.
func trimIncompleteUTF8Suffix(data []byte) []byte {
	for trimmed := 0; trimmed < utf8.UTFMax && len(data) > 0; trimmed++ {
		r, size := utf8.DecodeLastRune(data)
		if r != utf8.RuneError || size > 1 {
			return data
		}
		data = data[:len(data)-1]
	}
	return data
}

// trimIncompleteUTF8SuffixString is the string-native equivalent of
// trimIncompleteUTF8Suffix, avoiding a []byte/string round-trip for callers
// that already hold a string.
func trimIncompleteUTF8SuffixString(s string) string {
	for trimmed := 0; trimmed < utf8.UTFMax && len(s) > 0; trimmed++ {
		r, size := utf8.DecodeLastRuneInString(s)
		if r != utf8.RuneError || size > 1 {
			return s
		}
		s = s[:len(s)-1]
	}
	return s
}

// extensionFromContentType maps a Content-Type value to a file extension,
// including the leading dot.
func extensionFromContentType(contentType string) string {
	switch cleanContentType(contentType) {
	case "application/json":
		return ".json"
	case "application/yaml", "application/x-yaml":
		return ".yaml"
	case "text/plain":
		return ".txt"
	case "text/csv":
		return ".csv"
	case "text/markdown":
		return ".md"
	case "application/xml", "text/xml":
		return ".xml"
	case "application/javascript":
		return ".js"
	case "application/typescript", "text/typescript":
		return ".ts"
	case "text/html", "application/xhtml+xml":
		// HTML content is converted to markdown by wonton before being
		// saved, so it is stored with a .md extension.
		return ".md"
	default:
		return ".txt"
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
