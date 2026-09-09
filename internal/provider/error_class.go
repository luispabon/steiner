package provider

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
)

// errorClass buckets a provider call error into a small, aggregable set of
// causes for the provider diagnostics stream (issue #707). It is distinct
// from retryDecision: a class describes why a call ended, not whether the
// engine should try again.
type errorClass string

const (
	errorClassTimeout          errorClass = "timeout"
	errorClassConnectionReset  errorClass = "connection_reset"
	errorClassHTTP4xx          errorClass = "http_4xx"
	errorClassHTTP5xx          errorClass = "http_5xx"
	errorClassRateLimited      errorClass = "rate_limited"
	errorClassDecodeError      errorClass = "decode_error"
	errorClassContextCancelled errorClass = "context_cancelled"
	errorClassOther            errorClass = "other"
)

// maxErrorLength bounds streamErrorRecord.Error so a verbose provider error
// body cannot turn the provider stream into unbounded content.
const maxErrorLength = 200

// boundedError returns err's message truncated to maxErrorLength. Empty for a
// nil error.
func boundedError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > maxErrorLength {
		return s[:maxErrorLength]
	}
	return s
}

// classifyErrorClass buckets err for the provider diagnostics stream. ctx is
// the call's own context: when it is already done, the call ended because the
// caller cancelled or its deadline passed, which is context_cancelled rather
// than timeout even though a context-deadline error and a transport timeout
// both surface as similar-looking errors.
func classifyErrorClass(ctx context.Context, err error) errorClass {
	if err == nil {
		return ""
	}
	if ctx != nil && ctx.Err() != nil {
		return errorClassContextCancelled
	}
	if isDecodeError(err) {
		return errorClassDecodeError
	}
	if httpErr := asHTTPError(err); httpErr != nil {
		switch {
		case httpErr.StatusCode == http.StatusTooManyRequests:
			return errorClassRateLimited
		case httpErr.StatusCode >= 500:
			return errorClassHTTP5xx
		case httpErr.StatusCode >= 400:
			return errorClassHTTP4xx
		}
	}
	if isTimeoutError(err) {
		return errorClassTimeout
	}
	if isConnectionResetError(err) {
		return errorClassConnectionReset
	}
	return errorClassOther
}

func isDecodeError(err error) bool {
	return errors.Is(err, errDecodeChatCompletionResponse) ||
		errors.Is(err, errDecodeStreamChunk) ||
		errors.Is(err, errDecodeStreamChunkUnexpected) ||
		errors.Is(err, errDecodeToolCallArguments)
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "timeout")
}

func isConnectionResetError(err error) bool {
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "connection reset by peer"):
		return true
	case strings.Contains(text, "broken pipe"):
		return true
	case strings.Contains(text, "unexpected eof"):
		return true
	case strings.Contains(text, "stream error"):
		return true
	default:
		return false
	}
}
