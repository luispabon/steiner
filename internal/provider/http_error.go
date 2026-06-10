package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// HTTPError captures a non-success provider HTTP response.
type HTTPError struct {
	StatusCode int
	Status     string
	Body       string
	Header     http.Header
}

func (e *HTTPError) Error() string {
	if e == nil {
		return "<nil>"
	}
	id := e.Status
	if id == "" {
		id = strconv.Itoa(e.StatusCode)
	}
	if strings.TrimSpace(e.Body) == "" {
		return fmt.Sprintf("chat completions request failed: %s", id)
	}
	return fmt.Sprintf("chat completions request failed: %s: %s", id, e.Body)
}

func isRetryableHTTPStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func isRetryableTransportError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if retryableTransportErrorText(err) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	type temporary interface{ Temporary() bool }
	var tmpErr temporary
	if errors.As(err, &tmpErr) && tmpErr.Temporary() {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Err != nil {
		return isRetryableTransportError(opErr.Err)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return isRetryableTransportError(urlErr.Err)
	}
	return false
}

func retryAfterDelay(header http.Header, max time.Duration) (time.Duration, bool) {
	if header == nil {
		return 0, false
	}
	raw := strings.TrimSpace(header.Get("Retry-After"))
	if raw == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds < 0 {
			return 0, false
		}
		delay := time.Duration(seconds) * time.Second
		if delay <= 0 {
			return 0, false
		}
		if max > 0 && delay > max {
			delay = max
		}
		return delay, true
	}
	when, err := http.ParseTime(raw)
	if err != nil {
		return 0, false
	}
	delay := time.Until(when)
	if delay <= 0 {
		return 0, false
	}
	if max > 0 && delay > max {
		delay = max
	}
	return delay, true
}

// RetryableProviderError reports whether err is a transient provider error
// that may succeed on a subsequent attempt. When true it returns a suggested
// delay parsed from the response (zero when no delay hint is available).
func RetryableProviderError(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		return 0, false
	}
	if !isRetryableHTTPStatus(httpErr.StatusCode) {
		return 0, false
	}
	delay, hasHeader := retryAfterDelay(httpErr.Header, 0)
	if !hasHeader && httpErr.StatusCode == http.StatusTooManyRequests {
		if parsed, ok := parseLiteLLMRetryAfter(httpErr.Body); ok {
			delay = parsed
		}
	}
	return delay, true
}

// IsRateLimitError reports whether err is a 429 Too Many Requests error.
func IsRateLimitError(err error) bool {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode == http.StatusTooManyRequests
}

func retryableTransportErrorText(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "unexpected eof"):
		return true
	case strings.Contains(text, "connection reset by peer"):
		return true
	case strings.Contains(text, "broken pipe"):
		return true
	case strings.Contains(text, "http/2 stream error"):
		return true
	case strings.Contains(text, "http2: stream error"):
		return true
	case strings.Contains(text, "stream error"):
		return true
	default:
		return false
	}
}
