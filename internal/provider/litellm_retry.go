// Package provider contains litellm-specific retry-after body parsing.
// This file handles litellm-specific 429 error body interpretation,
// including extraction of retry delays from error messages and detection
// of non-retryable budget exhaustion.
package provider

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	// litellmRetryAfterTextPattern matches "Try again in N seconds" in error messages
	litellmRetryAfterTextPattern = regexp.MustCompile(`\bTry again in (\d+) seconds\b`)
	// litellmRetryAfterJSONPattern matches "retry_after": N in JSON bodies
	litellmRetryAfterJSONPattern = regexp.MustCompile(`"retry_after":\s*(\d+)`)
)

// parseLiteLLMRetryAfter extracts the retry delay from a litellm 429 error body.
// litellm embeds "Try again in N seconds" in error messages when relaying
// upstream rate-limit responses. Returns (0, false) on any parse failure.
func parseLiteLLMRetryAfter(body string) (time.Duration, bool) {
	// Try text pattern first: "Try again in N seconds"
	matches := litellmRetryAfterTextPattern.FindStringSubmatch(body)
	if len(matches) >= 2 {
		n, err := strconv.Atoi(matches[1])
		if err == nil {
			return time.Duration(n) * time.Second, true
		}
	}

	// Try JSON pattern: "retry_after": N
	matches = litellmRetryAfterJSONPattern.FindStringSubmatch(body)
	if len(matches) >= 2 {
		n, err := strconv.Atoi(matches[1])
		if err == nil {
			return time.Duration(n) * time.Second, true
		}
	}

	return 0, false
}

// isLiteLLMBudgetExceeded reports whether a litellm 429 body indicates
// budget exhaustion, which is non-retryable.
func isLiteLLMBudgetExceeded(body string) bool {
	return strings.Contains(strings.ToLower(body), "budget has been exceeded")
}
