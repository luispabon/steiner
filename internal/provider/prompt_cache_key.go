package provider

import (
	"crypto/rand"
	"fmt"
)

// NewPromptCacheKey returns a random 128-bit hex identifier suitable for
// ChatRequest.PromptCacheKey. Run modes with a persisted session ID (e.g.
// interactive sessions, one-shot runs) should reuse that stable ID instead;
// this is for modes that have no such ID (single-shot exec, sub-agent runs).
// Callers that prefer to disable caching rather than fail on an entropy error
// may discard the error and use the empty string, which leaves PromptCacheKey
// unset.
func NewPromptCacheKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate prompt cache key: %w", err)
	}
	return fmt.Sprintf("%032x", b), nil
}
