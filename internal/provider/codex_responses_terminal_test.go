package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestResponsesStreamTerminalEvents(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantErr    string
		wantFinish string
		wantText   string
	}{
		{
			name: "incomplete carries finish reason",
			body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
				"data: {\"type\":\"response.incomplete\",\"response\":{\"output\":[],\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n",
			wantFinish: "max_output_tokens",
			wantText:   "partial",
		},
		{
			name:    "failed with message and code",
			body:    "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"model exploded\"}}}\n\n",
			wantErr: "model exploded (code server_error)",
		},
		{
			name:    "failed without error object",
			body:    "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\"}}\n\n",
			wantErr: "responses stream failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks, err := collectResponsesStreamChunks(t, strings.NewReader(tt.body))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				if !errors.Is(err, errResponsesStreamFailed) {
					t.Fatalf("error %v does not wrap errResponsesStreamFailed", err)
				}
				if decision := classifyProviderError(err, time.Second); decision.retry {
					t.Fatalf("classifyProviderError retry = true, want false: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
			final := chunks[len(chunks)-1]
			if !final.Done {
				t.Fatal("final chunk Done = false, want true")
			}
			if final.FinishReason != tt.wantFinish {
				t.Fatalf("FinishReason = %q, want %q", final.FinishReason, tt.wantFinish)
			}
			if final.Delta.Content != tt.wantText {
				t.Fatalf("content = %q, want %q", final.Delta.Content, tt.wantText)
			}
		})
	}
}

func TestClassifyProviderErrorFailedResponseIgnoresRetryableText(t *testing.T) {
	var resp responsesResponse
	if err := json.Unmarshal([]byte(`{"error":{"message":"upstream timeout while generating"}}`), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	err := responsesFailedError(resp)
	if decision := classifyProviderError(err, time.Second); decision.retry {
		t.Fatalf("retry = true, want false for %v", err)
	}
}

func TestCodexWSTerminalEvents(t *testing.T) {
	tests := []struct {
		name       string
		terminal   map[string]any
		wantErr    string
		wantFinish string
	}{
		{
			name: "incomplete finishes turn",
			terminal: map[string]any{
				"type": "response.incomplete",
				"response": map[string]any{
					"output":             []any{},
					"status":             "incomplete",
					"incomplete_details": map[string]any{"reason": "max_output_tokens"},
				},
			},
			wantFinish: "max_output_tokens",
		},
		{
			name: "failed errors without resend",
			terminal: map[string]any{
				"type": "response.failed",
				"response": map[string]any{
					"status": "failed",
					"error":  map[string]any{"code": "server_error", "message": "model exploded"},
				},
			},
			wantErr: "model exploded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newWSTestServer(t, nil, func(conn *websocket.Conn, _ int) {
				if !wsServerRead(t, conn) {
					return
				}
				wsServerWrite(t, conn, tt.terminal)
				// Hold the socket open: a client that keeps waiting for more
				// frames would hang here instead of returning.
				time.Sleep(3 * time.Second)
			})
			provider := newTestWSProvider(t, server.wsURL())

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			stream, err := provider.StreamChatCompletion(ctx, ChatRequest{
				Model:    "test-model",
				Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
			})
			if err != nil {
				t.Fatalf("StreamChatCompletion: %v", err)
			}
			chunks := drainChunks(stream)
			if len(chunks) == 0 {
				t.Fatal("no chunks received")
			}
			final := chunks[len(chunks)-1]
			if tt.wantErr != "" {
				if final.Error == "" || !strings.Contains(final.Error, tt.wantErr) {
					t.Fatalf("final = %#v, want error containing %q", final, tt.wantErr)
				}
			} else {
				if final.Error != "" || !final.Done {
					t.Fatalf("final = %#v, want clean Done chunk", final)
				}
				if final.FinishReason != tt.wantFinish {
					t.Fatalf("FinishReason = %q, want %q", final.FinishReason, tt.wantFinish)
				}
			}
			if got := server.connCount(); got != 1 {
				t.Fatalf("connections = %d, want 1 (no redial)", got)
			}
		})
	}
}
