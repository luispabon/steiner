package builtin

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/deepnoodle-ai/wonton/fetch"
)

// errorBodySnippetRunes bounds the response body snippet captured on
// non-200 fetch_url errors, so a large error page doesn't blow out the
// tool result.
const errorBodySnippetRunes = 500

// FetchURLError is an error result from a fetch_url tool call.
type FetchURLError struct {
	URL        string `json:"url"`
	Error      string `json:"error"`
	StatusCode int    `json:"status_code,omitempty"`
	RetryAfter string `json:"retry_after,omitempty"`
	Body       string `json:"body,omitempty"` // bounded snippet
}

// oversizedBodyErrorSubstring is the string wonton's HTTPFetcher uses when
// the response body exceeds MaxBodySize.
const oversizedBodyErrorSubstring = "response size exceeds limit"

// unexpectedContentTypeErrorPrefix is the prefix wonton's HTTPFetcher uses
// when the response's Content-Type is not text/html. The content type it
// rejected follows the prefix verbatim.
const unexpectedContentTypeErrorPrefix = "unexpected content type: "

// handleFetchError classifies an error from wonton/fetch's HTTPFetcher and
// builds the appropriate fetch_url result. wonton exposes neither of these
// failure modes as a sentinel or typed error, so string matching against its
// message is the only option for both; each match is pinned by a test so a
// wonton reword fails loudly rather than silently regressing to an opaque
// generic error.
func handleFetchError(ctx context.Context, httpClient *http.Client, in FetchURLInput, workDir string, err error) (any, error) {
	if isOversizedBodyError(err) {
		return newFetchURLError(in.URL, oversizeBodyErrorMessage(in.MaxSize)), nil
	}
	// Reachable when the HEAD preflight returned no usable Content-Type
	// (hosts that 405 on HEAD, or block it entirely) and the origin then
	// serves a response wonton rejects: it checks Content-Type before ever
	// reading the status code, headers, or content, so falling straight
	// through to err.Error() below would lose all of that. The recovered
	// content type is routed through the exact same decision the normal
	// path makes (routeByContentType), so a type discovered late — an
	// image, a text format, or something unsupported — is handled
	// identically to one HEAD reported up front, rather than always being
	// forced through fetchRawText. Pinned by
	// TestFetchURLUnexpectedContentTypeFallsBackToRawText and
	// TestFetchURLUnexpectedContentTypeRecoversImage.
	if contentType, ok := unexpectedContentType(err); ok {
		if result, handled, resErr := routeByContentType(ctx, httpClient, in, workDir, contentType); handled {
			return result, resErr
		}
		// contentType was empty or unclassifiable and the URL didn't
		// suggest an image; wonton already tried and rejected this
		// response, so there is no HTML pipeline left to fall through to.
		// fetchRawText is the best remaining option: it still recovers
		// StatusCode, RetryAfter, and a bounded Body snippet.
		return fetchRawText(ctx, httpClient, in, workDir, contentType)
	}
	return newFetchURLError(in.URL, err.Error()), nil
}

// newFetchURLError builds a simple message-only fetch_url error result.
func newFetchURLError(url, msg string) *FetchURLError {
	return &FetchURLError{URL: url, Error: msg}
}

// newFetchURLHTTPError builds a fetch_url error result for a non-200 HTML
// response, carrying the status code, Retry-After header, and a bounded
// body snippet through to the model.
func newFetchURLHTTPError(url string, resp *fetch.Response) *FetchURLError {
	return &FetchURLError{
		URL:        url,
		Error:      fmt.Sprintf("HTTP %d", resp.StatusCode),
		StatusCode: resp.StatusCode,
		RetryAfter: resp.Headers["Retry-After"],
		Body:       boundedRuneSnippet(resp.Markdown, errorBodySnippetRunes),
	}
}

// isOversizedBodyError reports whether err is wonton's oversized-body error.
// Pinned by TestFetchURLOversizedHTMLBodyReturnsMaxSizeError.
func isOversizedBodyError(err error) bool {
	return strings.Contains(err.Error(), oversizedBodyErrorSubstring)
}

// oversizeBodyErrorMessage builds the advisory for an oversized-body error.
// When maxSize is below the schema ceiling, raising max_size is genuinely
// actionable advice. When maxSize is already at the ceiling (the default),
// telling the model to raise it further is not: it would retry against the
// same ceiling and get the identical error. In that case, point it at
// fetching a narrower resource instead.
func oversizeBodyErrorMessage(maxSize int) string {
	if maxSize < maxFetchURLMaxSize {
		return fmt.Sprintf("response body exceeded max_size (%d bytes); retry with a larger max_size or fetch a smaller resource", maxSize)
	}
	return fmt.Sprintf("response body exceeded max_size (%d bytes), which is already the fetch ceiling; fetch a narrower resource instead (e.g. a specific page or sub-resource)", maxSize)
}

// unexpectedContentType reports whether err is wonton's unexpected-content-type
// error and, if so, extracts the content type it rejected.
func unexpectedContentType(err error) (string, bool) {
	msg := err.Error()
	if !strings.HasPrefix(msg, unexpectedContentTypeErrorPrefix) {
		return "", false
	}
	return strings.TrimPrefix(msg, unexpectedContentTypeErrorPrefix), true
}

// boundedRuneSnippet returns s truncated to at most maxRunes runes.
func boundedRuneSnippet(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes])
}

// appendAdvisory composes advisory into existing, separated by a space if
// both are non-empty. Used to combine the main-content fallback notice, the
// nav-like warning, and the disk-save message into a single Message.
func appendAdvisory(existing, advisory string) string {
	if advisory == "" {
		return existing
	}
	if existing == "" {
		return advisory
	}
	return existing + " " + advisory
}
