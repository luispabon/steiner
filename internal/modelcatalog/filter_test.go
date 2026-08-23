package modelcatalog

import "testing"

func TestHeuristicallyExcluded(t *testing.T) {
	tests := []struct {
		id      string
		exclude bool
	}{
		{"text-embedding-3-small", true},
		{"dall-e-3", true},
		{"gpt-image-1", true},
		{"whisper-1", true},
		{"tts-1", true},
		{"gpt-4o-mini-tts", true},
		{"text-moderation-latest", true},
		{"omni-moderation-latest", true},
		{"babbage-002", true},
		{"davinci-002", true},
		{"my-embed-model", true},
		{"my-rerank-model", true},
		{"my-guard-model", true},
		{"GPT-4.1", false},
		{"claude-sonnet", false},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			if got := HeuristicallyExcluded(test.id); got != test.exclude {
				t.Fatalf("HeuristicallyExcluded: got %v, want %v", got, test.exclude)
			}
		})
	}
}
