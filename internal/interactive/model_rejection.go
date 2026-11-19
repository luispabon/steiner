package interactive

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/luispabon/steiner/internal/provider"
)

// modelRejectionGuidance returns a short suggestion when err says the provider
// rejected the model itself, or empty when the error is something else. It
// never retries or switches models: it only tells the user which levers exist.
func modelRejectionGuidance(model string, fallback string, err error) string {
	var httpErr *provider.HTTPError
	if !errors.As(err, &httpErr) || !rejectedModelStatus(httpErr.StatusCode) {
		return ""
	}
	if !rejectedModelBody(httpErr.Body) {
		return ""
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "the current model"
	} else {
		model = fmt.Sprintf("model %q", model)
	}
	guidance := fmt.Sprintf("The provider rejected %s. Change it with /model", model)
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		guidance += fmt.Sprintf(", or /compact to compact the conversation and continue on the configured compaction fallback %q", fallback)
	}
	return guidance + "."
}

// rejectedModelStatus lists the HTTP statuses a provider uses when it refuses
// a model name or a model capability.
func rejectedModelStatus(status int) bool {
	switch status {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
		return true
	default:
		return false
	}
}

// rejectedModelBody reports whether the error body names a model problem. It
// keeps the match narrow so unrelated 400/404 responses get no guidance.
func rejectedModelBody(body string) bool {
	text := strings.ToLower(body)
	if !strings.Contains(text, "model") {
		return false
	}
	for _, marker := range []string{"not found", "does not exist", "not supported", "unsupported", "invalid", "unknown", "retired", "decommissioned", "deprecated"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// runErrorMessage formats a failed run for the stop-reason event, appending
// model rejection guidance when the provider refused the model.
func (s *Session) runErrorMessage(err error) string {
	message := fmt.Sprintf("Error: %v", err)
	s.mu.RLock()
	model := effectiveModelRef(s.deps.Config)
	fallback := s.deps.Config.Models.Effective.CompactionFallback
	s.mu.RUnlock()
	if guidance := modelRejectionGuidance(model, fallback, err); guidance != "" {
		message += "\n" + guidance
	}
	return message
}
