package output

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewModelCallFinishedEventPromptCacheFields(t *testing.T) {
	t.Parallel()
	coldAt := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		params      ModelCallFinishedParams
		wantSuccess bool
		wantColdAt  time.Time
	}{
		{
			name:        "successful call carries the deadline",
			params:      ModelCallFinishedParams{Turn: 1, Success: true, CacheColdAt: coldAt},
			wantSuccess: true,
			wantColdAt:  coldAt,
		},
		{
			name:   "cancelled call carries neither",
			params: ModelCallFinishedParams{Turn: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			event := NewModelCallFinishedEvent(tt.params)
			payload, ok := event.Payload.(ModelCallFinishedEvent)
			if !ok {
				t.Fatalf("payload type = %T, want ModelCallFinishedEvent", event.Payload)
			}
			if payload.Success != tt.wantSuccess {
				t.Errorf("Success = %v, want %v", payload.Success, tt.wantSuccess)
			}
			if !payload.CacheColdAt.Equal(tt.wantColdAt) {
				t.Errorf("CacheColdAt = %v, want %v", payload.CacheColdAt, tt.wantColdAt)
			}

			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			jsonText := string(encoded)
			if tt.wantSuccess {
				if !strings.Contains(jsonText, `"success":true`) {
					t.Errorf("json = %s, want success true", jsonText)
				}
				if !strings.Contains(jsonText, `"cache_cold_at":"2026-11-12T10:00:00Z"`) {
					t.Errorf("json = %s, want the cache cold deadline", jsonText)
				}
				return
			}
			for _, absent := range []string{"success", "cache_cold_at"} {
				if strings.Contains(jsonText, absent) {
					t.Errorf("json = %s, want no %q field", jsonText, absent)
				}
			}
		})
	}
}
