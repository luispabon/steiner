package agent

import (
	"testing"
	"time"
)

func TestCompactionCacheCold(t *testing.T) {
	t.Parallel()
	last := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	recorded := LastMainRequest{At: last, ProviderAlias: "openai", BackendModelID: "gpt-test", PromptCacheKey: "cache-key"}
	const after = 5 * time.Minute

	tests := []struct {
		name  string
		now   time.Time
		last  LastMainRequest
		after time.Duration
		want  bool
	}{
		{name: "zero threshold is never cold", now: last.Add(time.Hour), last: recorded, after: 0, want: false},
		{name: "negative threshold is never cold", now: last.Add(time.Hour), last: recorded, after: -time.Second, want: false},
		{name: "non-positive threshold ignores missing request", now: last, last: LastMainRequest{}, after: 0, want: false},
		{name: "missing request is cold", now: last, last: LastMainRequest{}, after: after, want: true},
		{name: "one nanosecond before threshold is warm", now: last.Add(after - time.Nanosecond), last: recorded, after: after, want: false},
		{name: "exactly at threshold is warm", now: last.Add(after), last: recorded, after: after, want: false},
		{name: "one nanosecond past threshold is cold", now: last.Add(after + time.Nanosecond), last: recorded, after: after, want: true},
		{name: "well past threshold is cold", now: last.Add(time.Hour), last: recorded, after: after, want: true},
		{name: "now before the request is warm", now: last.Add(-time.Minute), last: recorded, after: after, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := compactionCacheCold(tt.now, tt.last, tt.after); got != tt.want {
				t.Errorf("compactionCacheCold() = %v, want %v", got, tt.want)
			}
		})
	}
}
