package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

// modelCallFinishedPayloads returns the ModelCallFinishedEvent payloads in
// events, in emission order.
func modelCallFinishedPayloads(events []output.Event) []output.ModelCallFinishedEvent {
	var out []output.ModelCallFinishedEvent
	for _, event := range events {
		if payload, ok := event.Payload.(output.ModelCallFinishedEvent); ok {
			out = append(out, payload)
		}
	}
	return out
}

// TestModelCallFinishedCarriesPromptCacheDeadline proves the successful main
// call emits the prompt cache expiry estimate (request start plus
// PromptCacheTTL) when the TTL is positive, and a zero deadline when it is not.
// The estimate does not depend on the compaction fallback.
func TestModelCallFinishedCarriesPromptCacheDeadline(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	resolve := func(context.Context) (CompactionTarget, error) {
		return CompactionTarget{}, errors.New("resolution must not run for a main call")
	}
	tests := []struct {
		name       string
		ttl        time.Duration
		fallback   CompactionFallback
		wantColdAt time.Time
	}{
		{
			name:       "positive ttl sets the deadline without a fallback",
			ttl:        5 * time.Minute,
			wantColdAt: start.Add(5 * time.Minute),
		},
		{
			name: "zero ttl disables tracking without a fallback",
		},
		{
			name:     "zero ttl stays off with a fallback",
			fallback: CompactionFallback{After: 5 * time.Minute, Resolve: resolve},
		},
		{
			name:       "ttl wins over a different fallback delay",
			ttl:        50 * time.Minute,
			fallback:   CompactionFallback{After: 5 * time.Minute, Resolve: resolve},
			wantColdAt: start.Add(50 * time.Minute),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var events []output.Event
			prov := &fakeProvider{responses: []provider.ChatResponse{{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "ok"},
				FinishReason: "stop",
			}}}
			req := mainRequestRun(prov, &steppingClock{base: start})
			req.Events = output.SinkFunc(func(event output.Event) { events = append(events, event) })
			req.CompactionFallback = tt.fallback
			req.PromptCacheTTL = tt.ttl

			if _, err := NewRunner().Run(context.Background(), req); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			finished := modelCallFinishedPayloads(events)
			if len(finished) != 1 {
				t.Fatalf("model call finished events = %d, want 1", len(finished))
			}
			if !finished[0].Success {
				t.Error("Success = false for a completed call, want true")
			}
			if got := finished[0].CacheColdAt; !got.Equal(tt.wantColdAt) {
				t.Errorf("CacheColdAt = %v, want %v", got, tt.wantColdAt)
			}
		})
	}
}

// TestModelCallFinishedFailureAndCancellationAreNotSuccess proves failure and
// cancellation emissions leave Success false and the deadline zero, so the TUI
// never mistakes them for a fresh estimate.
func TestModelCallFinishedFailureAndCancellationAreNotSuccess(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

	t.Run("provider error", func(t *testing.T) {
		t.Parallel()
		var events []output.Event
		prov := &fakeProvider{chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
			return provider.ChatResponse{}, errors.New("upstream failed")
		}}
		req := mainRequestRun(prov, &steppingClock{base: start})
		req.Events = output.SinkFunc(func(event output.Event) { events = append(events, event) })
		req.CompactionFallback = CompactionFallback{After: 5 * time.Minute, Resolve: func(context.Context) (CompactionTarget, error) {
			return CompactionTarget{}, nil
		}}
		req.PromptCacheTTL = 5 * time.Minute

		if _, err := NewRunner().Run(context.Background(), req); err == nil {
			t.Fatal("Run() error = nil, want provider error")
		}
		finished := modelCallFinishedPayloads(events)
		if len(finished) != 1 {
			t.Fatalf("model call finished events = %d, want 1", len(finished))
		}
		if finished[0].Success {
			t.Error("Success = true for a failed call, want false")
		}
		if !finished[0].CacheColdAt.IsZero() {
			t.Errorf("CacheColdAt = %v for a failed call, want zero", finished[0].CacheColdAt)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		t.Parallel()
		var events []output.Event
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		prov := &fakeProvider{chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
			cancel()
			return provider.ChatResponse{}, ctx.Err()
		}}
		req := mainRequestRun(prov, &steppingClock{base: start})
		req.Events = output.SinkFunc(func(event output.Event) { events = append(events, event) })
		req.CompactionFallback = CompactionFallback{After: 5 * time.Minute, Resolve: func(context.Context) (CompactionTarget, error) {
			return CompactionTarget{}, nil
		}}
		req.PromptCacheTTL = 5 * time.Minute

		if _, err := NewRunner().Run(ctx, req); err != nil {
			t.Fatalf("Run() error = %v, want a cancelled turn without error", err)
		}
		finished := modelCallFinishedPayloads(events)
		if len(finished) != 1 {
			t.Fatalf("model call finished events = %d, want 1", len(finished))
		}
		if finished[0].Success {
			t.Error("Success = true for a cancelled call, want false")
		}
		if !finished[0].CacheColdAt.IsZero() {
			t.Errorf("CacheColdAt = %v for a cancelled call, want zero", finished[0].CacheColdAt)
		}
	})
}
