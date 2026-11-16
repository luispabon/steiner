package provider

import (
	"encoding/json"
	"log/slog"
	"math"
	"time"
)

// This file decodes the CLI's own `system/api_retry` notices. The CLI retries a
// retryable API failure itself, before it produces any assistant output, and
// reports each retry with an attempt number, the permitted retry count, the
// delay, the HTTP status when there was a response, and a failure category. The
// notice carries no assistant content, so it becomes a provider diagnostic the
// user sees rather than a decode error or a text delta.

// claudeSubAPIRetry is one decoded CLI API retry notice.
type claudeSubAPIRetry struct {
	// Attempt is the retry number, counting from 1.
	Attempt int
	// MaxRetries is the total number of retries the CLI permits for this cause.
	MaxRetries int
	// RetryDelay is how long the CLI waits before the next attempt.
	RetryDelay time.Duration
	// ErrorStatus is the HTTP status of the failed request, or 0 when the CLI
	// reported no response.
	ErrorStatus int
	// Error is the CLI's failure category, such as "rate_limit".
	Error string
}

// claudeSubAPIRetryPayload is the wire form of one api_retry envelope.
type claudeSubAPIRetryPayload struct {
	Attempt      int    `json:"attempt"`
	MaxRetries   int    `json:"max_retries"`
	RetryDelayMS int64  `json:"retry_delay_ms"`
	ErrorStatus  *int   `json:"error_status"`
	Error        string `json:"error"`
}

// decodeSystem converts one `system` envelope into decoded events. Only the CLI's
// API retry is surfaced, and only while the phase is streaming: the CLI retries
// before it produces output, so a notice after message_stop or the terminal
// result cannot be attributed to a live request.
func (d *claudeSubDecoder) decodeSystem(ev claudeSubEvent) []claudeSubDecoded {
	if ev.Subtype != "api_retry" || d.phase != claudeSubPhaseStreaming {
		return nil
	}
	return decodeClaudeSubAPIRetry(ev.Raw)
}

// claudeSubMaxRetryDelayMS bounds the delay converted from the CLI's
// milliseconds, so a malformed value cannot overflow time.Duration.
const claudeSubMaxRetryDelayMS = int64(math.MaxInt64 / int64(time.Millisecond))

// decodeClaudeSubAPIRetry converts one api_retry envelope into a decode item. A
// malformed notice is dropped with a debug record instead of failing the turn: it
// carries no assistant content, so losing it cannot lose output.
func decodeClaudeSubAPIRetry(raw json.RawMessage) []claudeSubDecoded {
	var payload claudeSubAPIRetryPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		slog.Debug("claude_subscription api_retry notice ignored", "error", err)
		return nil
	}
	if payload.Attempt < 1 {
		slog.Debug("claude_subscription api_retry notice ignored", "attempt", payload.Attempt)
		return nil
	}
	retry := &claudeSubAPIRetry{
		Attempt:    payload.Attempt,
		MaxRetries: payload.MaxRetries,
		RetryDelay: time.Duration(min(max(payload.RetryDelayMS, 0), claudeSubMaxRetryDelayMS)) * time.Millisecond,
		Error:      payload.Error,
	}
	if payload.ErrorStatus != nil {
		retry.ErrorStatus = *payload.ErrorStatus
	}
	return []claudeSubDecoded{{Kind: claudeSubDecodeAPIRetry, Retry: retry}}
}
