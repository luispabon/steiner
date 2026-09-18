package modelcatalog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// F267: enumeratePages must detect repeated cursors and enforce page limits to prevent infinite loops.
func TestAnthropicEnumerateDetectsRepeatedCursor(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		afterID := r.URL.Query().Get("after_id")
		if afterID == "" {
			_, _ = w.Write([]byte(`{
				"data": [{"id": "claude-1", "display_name": "Claude 1", "max_input_tokens": 100000}],
				"has_more": true,
				"last_id": "first-cursor"
			}`))
			return
		}
		if afterID == "first-cursor" && requests == 2 {
			_, _ = w.Write([]byte(`{
				"data": [{"id": "claude-2", "display_name": "Claude 2", "max_input_tokens": 100000}],
				"has_more": true,
				"last_id": "first-cursor"
			}`))
			return
		}
		t.Errorf("unexpected cursor: %q", afterID)
	}))
	defer server.Close()

	_, err := NewAnthropicEnumerator(server.Client()).Enumerate(context.Background(), Endpoint{
		Alias:   "anthropic",
		Type:    "anthropic",
		BaseURL: server.URL,
		APIKey:  "key",
	}, EnumerationOptions{})
	if err == nil {
		t.Fatal("Enumerate should have returned an error for repeated cursor")
	}
	if requests != 2 {
		t.Fatalf("expected 2 requests, got %d", requests)
	}
}

// F267: enumeratePages must enforce a page limit to prevent unbounded loops.
func TestAnthropicEnumerateEnforcesPageCap(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		lastID := "cursor-" + fmt.Sprintf("%05d", requests)
		_, _ = w.Write([]byte(`{
			"data": [{"id": "claude-m","display_name": "Claude", "max_input_tokens": 100000, "capabilities": {"effort": {"low": {"supported": true}}}}],
			"has_more": true,
			"last_id": "` + lastID + `"
		}`))
	}))
	defer server.Close()

	enumerator := NewAnthropicEnumerator(server.Client())
	_, _, err := enumerator.enumeratePages(context.Background(), Endpoint{
		Alias:   "anthropic",
		Type:    "anthropic",
		BaseURL: server.URL,
		APIKey:  "key",
	}, server.URL, 20)
	if err == nil {
		t.Fatal("enumeratePages should have returned an error when page cap is exceeded")
	}
	if requests != anthropicMaxPages {
		t.Fatalf("expected %d requests (page cap), got %d", anthropicMaxPages, requests)
	}
}

// F269: Enumerate must return an error when the page cap is exceeded, not silently return a partial catalog.
func TestOpenRouterEnumerateReturnsErrorOnPageCapExceeded(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		nextPageNum := fmt.Sprintf("%05d", requests)
		nextURL := "/api/v1/models?page=" + nextPageNum
		_, _ = w.Write([]byte(`{
			"data": [{"id": "model-` + nextPageNum + `", "architecture": {"output_modalities": ["text"]}}],
			"links": {"next": "` + nextURL + `"}
		}`))
	}))
	defer server.Close()

	_, err := NewOpenRouterEnumerator(server.Client()).Enumerate(context.Background(), Endpoint{
		Type:    "openrouter",
		BaseURL: server.URL,
	}, EnumerationOptions{})
	if err == nil {
		t.Fatal("Enumerate should have returned an error when page cap is exceeded")
	}
	if requests != openRouterMaxPages {
		t.Fatalf("expected %d requests (page cap), got %d", openRouterMaxPages, requests)
	}
}

// F271: openRouterNonText must return false only when text modality is present, regardless of order.
func TestOpenRouterNonTextHandlesMixedModalities(t *testing.T) {
	tests := []struct {
		name         string
		architecture openRouterArchitecture
		wantNonText  bool
	}{
		{
			name: "text only is text-capable",
			architecture: openRouterArchitecture{
				OutputModalities: []string{"text"},
			},
			wantNonText: false,
		},
		{
			name: "embeddings only is non-text",
			architecture: openRouterArchitecture{
				OutputModalities: []string{"embeddings"},
			},
			wantNonText: true,
		},
		{
			name: "text and embeddings is text-capable",
			architecture: openRouterArchitecture{
				OutputModalities: []string{"embeddings", "text"},
			},
			wantNonText: false,
		},
		{
			name: "text first, embeddings second is text-capable",
			architecture: openRouterArchitecture{
				OutputModalities: []string{"text", "embeddings"},
			},
			wantNonText: false,
		},
		{
			name: "empty modalities with text->embeddings in modality field is non-text",
			architecture: openRouterArchitecture{
				Modality:         "text->embeddings",
				OutputModalities: []string{},
			},
			wantNonText: true,
		},
		{
			name: "empty modalities with embedding substring is non-text",
			architecture: openRouterArchitecture{
				Modality:         "embedding",
				OutputModalities: []string{},
			},
			wantNonText: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := openRouterNonText(tt.architecture); got != tt.wantNonText {
				t.Fatalf("openRouterNonText() = %v, want %v", got, tt.wantNonText)
			}
		})
	}
}
