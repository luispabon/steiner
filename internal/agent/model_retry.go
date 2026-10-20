package agent

import (
	"errors"
	"strings"

	"github.com/luispabon/steiner/internal/provider"
)

// imageRejectionMarkers are lowercase substrings of a provider 400 body that
// indicate the request was rejected because of its image content. A generic
// 400 (context length, malformed ordering, stream-required) must not latch the
// model vision-incapable for the rest of the process.
var imageRejectionMarkers = []string{"image", "vision", "multimodal", "multi-modal"}

func isImageRejection(body string) bool {
	lower := strings.ToLower(body)
	for _, marker := range imageRejectionMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func shouldRetryWithoutImages(err error, messages []provider.Message) bool {
	if len(messages) == 0 || !requestHasImages(messages) {
		return false
	}
	var httpErr *provider.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode == 400 && isImageRejection(httpErr.Body)
}

func requestHasImages(messages []provider.Message) bool {
	for _, msg := range messages {
		if len(msg.Images) > 0 {
			return true
		}
	}
	return false
}
