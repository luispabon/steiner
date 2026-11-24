package advisor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
)

// The advisor reads FinishReason from the Responses/Codex wire, so this runs the
// handler against a real Codex Responses client to cover that path end to end.
func TestHandlerTruncationMarkerFromResponsesWire(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantMarker bool
	}{
		{
			name:       "incomplete by max output tokens adds marker",
			body:       `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial advice"}]}]}`,
			wantMarker: true,
		},
		{
			name:       "incomplete by content filter has no marker",
			body:       `{"status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial advice"}]}]}`,
			wantMarker: false,
		},
		{
			name:       "completed has no marker",
			body:       `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial advice"}]}]}`,
			wantMarker: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			t.Cleanup(server.Close)

			prov, err := provider.NewCodexResponses(provider.ClientConfig{BaseURL: server.URL, APIKey: "test-key", Model: "advisor-model"})
			if err != nil {
				t.Fatalf("NewCodexResponses() error = %v", err)
			}
			handler := NewHandler(HandlerDeps{
				Provider: prov,
				Model:    provider.ResolvedModel{BackendModelID: "advisor-model"},
				Config:   Config{MaxUsesPerRun: 1},
			})
			ctx := agent.WithConversationSnapshot(context.Background(), []provider.Message{
				{Role: provider.MessageRoleUser, Content: "review this"},
			})
			got, err := handler(ctx, nil)
			if err != nil {
				t.Fatalf("handler() error = %v", err)
			}
			note, ok := got.(string)
			if !ok {
				t.Fatalf("handler() = %T, want string", got)
			}
			if !strings.Contains(note, "partial advice") {
				t.Fatalf("result missing advisor text: %q", note)
			}
			if hasMarker := strings.Contains(note, "[advisor response truncated"); hasMarker != tt.wantMarker {
				t.Fatalf("truncation marker present = %v, want %v: %q", hasMarker, tt.wantMarker, note)
			}
		})
	}
}
