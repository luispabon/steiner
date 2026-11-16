package provider

import (
	"encoding/json"
	"testing"
)

// TestClaudeSubEventIsOverageAPIRetry proves the CLI's api_retry notice is never
// an overage signal: it is resolved from its subtype before the notification
// probe, so a notice with non-string text fields cannot close the pool, while a
// malformed system envelope of any other kind still fails closed.
func TestClaudeSubEventIsOverageAPIRetry(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{
			name: "valid api retry",
			raw:  `{"type":"system","subtype":"api_retry","attempt":1,"max_retries":10,"retry_delay_ms":1500,"error_status":529,"error":"overloaded"}`,
		},
		{
			name: "malformed api retry with non-string fields",
			raw:  `{"type":"system","subtype":"api_retry","attempt":1,"message":{"text":"busy"},"text":["busy"],"content":7}`,
		},
		{
			name: "malformed notification still fails closed",
			raw:  `{"type":"system","subtype":"notification","message":{"text":"extra usage"}}`,
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := claudeSubEvent{Type: "system", Raw: json.RawMessage(tt.raw)}
			if got := claudeSubEventIsOverage(ev); got != tt.want {
				t.Errorf("claudeSubEventIsOverage(%s) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
