package provider

import (
	"encoding/json"
	"testing"
)

func TestNormalizeResponsesFinishReason(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "incomplete by max output tokens is length",
			body: `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`,
			want: "length",
		},
		{
			name: "incomplete by content filter is unchanged",
			body: `{"status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[]}`,
			want: "content_filter",
		},
		{
			name: "unknown incomplete reason is unchanged",
			body: `{"status":"incomplete","incomplete_details":{"reason":"run_cancelled"},"output":[]}`,
			want: "run_cancelled",
		},
		{
			name: "incomplete without details is stop",
			body: `{"status":"incomplete","output":[]}`,
			want: "stop",
		},
		{
			name: "completed is stop",
			body: `{"status":"completed","output":[]}`,
			want: "stop",
		},
		{
			name: "reason ignored unless status is incomplete",
			body: `{"status":"completed","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`,
			want: "stop",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload responsesResponse
			if err := json.Unmarshal([]byte(tt.body), &payload); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got, err := normalizeResponsesResponse(payload)
			if err != nil {
				t.Fatalf("normalizeResponsesResponse() error = %v", err)
			}
			if got.FinishReason != tt.want {
				t.Fatalf("FinishReason = %q, want %q", got.FinishReason, tt.want)
			}
		})
	}
}
