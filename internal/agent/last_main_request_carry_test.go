package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestCarryLastMainRequestRules proves the shared carry rule: a failed run that
// returned no request keeps the carried value, and every other outcome adopts
// the returned value, zero included.
func TestCarryLastMainRequestRules(t *testing.T) {
	t.Parallel()
	carried := lastMainRequestFixture(time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC))
	returned := lastMainRequestFixture(time.Date(2026, 10, 29, 9, 5, 0, 0, time.UTC))
	failure := errors.New("provider failed")
	tests := []struct {
		name     string
		returned LastMainRequest
		err      error
		want     LastMainRequest
	}{
		{name: "success with zero resets", want: LastMainRequest{}},
		{name: "success with a request adopts it", returned: returned, want: returned},
		{name: "failure with zero keeps the carried request", err: failure, want: carried},
		{name: "cancellation with zero keeps the carried request", err: context.Canceled, want: carried},
		{name: "failure with a request adopts it", returned: returned, err: failure, want: returned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := CarryLastMainRequest(carried, tt.returned, tt.err); got != tt.want {
				t.Errorf("CarryLastMainRequest(carried, %+v, %v) = %+v, want %+v", tt.returned, tt.err, got, tt.want)
			}
		})
	}
}
