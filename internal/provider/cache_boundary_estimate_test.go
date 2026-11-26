package provider

import (
	"context"
	"testing"
)

// clearCacheBoundary returns request with CacheBoundary cleared on every
// message, the projection of a flag-off run.
func clearCacheBoundary(request ChatRequest) ChatRequest {
	unmarked := request
	unmarked.Messages = withoutCacheBoundary(request.Messages)
	return unmarked
}

// markCacheBoundary returns request with CacheBoundary set on the one message
// at index.
func markCacheBoundary(request ChatRequest, index int) ChatRequest {
	marked := clearCacheBoundary(request)
	marked.Messages[index].CacheBoundary = true
	return marked
}

// TestCacheBoundaryDoesNotChangeTokenEstimates pins that the marker is invisible
// to both the semantic estimator and the heuristic fallback.
func TestCacheBoundaryDoesNotChangeTokenEstimates(t *testing.T) {
	cases := []struct {
		name    string
		request ChatRequest
		index   int
	}{
		{name: "tool batch", request: flagOffToolBatchRequest(), index: 3},
		{name: "image", request: flagOffImageRequest(), index: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			unmarked, err := EstimateChatRequestTokens(ctx, clearCacheBoundary(tc.request))
			if err != nil {
				t.Fatalf("EstimateChatRequestTokens(unmarked) error = %v", err)
			}
			marked, err := EstimateChatRequestTokens(ctx, markCacheBoundary(tc.request, tc.index))
			if err != nil {
				t.Fatalf("EstimateChatRequestTokens(marked) error = %v", err)
			}
			if marked != unmarked {
				t.Errorf("EstimateChatRequestTokens() = %d with CacheBoundary, want %d without", marked, unmarked)
			}
			if got, want := estimateHeuristicChatRequestTokens(markCacheBoundary(tc.request, tc.index)), estimateHeuristicChatRequestTokens(clearCacheBoundary(tc.request)); got != want {
				t.Errorf("estimateHeuristicChatRequestTokens() = %d with CacheBoundary, want %d without", got, want)
			}
		})
	}
}
