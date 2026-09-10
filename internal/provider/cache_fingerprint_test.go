package provider

import (
	"context"
	"net/url"
	"testing"
)

func TestResponsesWireCacheFingerprintUsesWirePrefixesAndHeaders(t *testing.T) {
	baseURL, _ := url.Parse("https://example.test/v1")
	wire := &responsesWire{baseURL: baseURL, apiKey: "secret"}
	request := ChatRequest{
		Model:          "model",
		PromptCacheKey: "cache-key",
		Messages: []Message{
			{Role: MessageRoleSystem, Content: "stable"},
			{Role: MessageRoleUser, Content: "shared"},
			{Role: MessageRoleUser, Content: "unique"},
		},
	}
	client := &Client{wire: wire}
	first, err := client.CacheFingerprint(context.Background(), request, false, 2, 2)
	if err != nil {
		t.Fatalf("CacheFingerprint() error = %v", err)
	}
	request.Messages[2].Content = "different unique"
	second, err := client.CacheFingerprint(context.Background(), request, false, 2, 2)
	if err != nil {
		t.Fatalf("CacheFingerprint() second error = %v", err)
	}
	if first.CacheablePrefixHash != second.CacheablePrefixHash {
		t.Fatalf("cacheable prefix hashes differ: %q vs %q", first.CacheablePrefixHash, second.CacheablePrefixHash)
	}
	if first.SharedPrefixHash != second.SharedPrefixHash {
		t.Fatalf("shared prefix hashes differ: %q vs %q", first.SharedPrefixHash, second.SharedPrefixHash)
	}
	if len(first.CacheablePrefixHash) != 32 || len(first.SharedPrefixHash) != 32 {
		t.Fatalf("hash lengths = %d, %d, want 32", len(first.CacheablePrefixHash), len(first.SharedPrefixHash))
	}
	if !first.AffinityKeysMatch || first.SessionIDHash == "" || first.ThreadIDHash == "" || first.OriginatorHash == "" {
		t.Fatalf("affinity diagnostics = %#v, want matching opaque header hashes", first)
	}
}
