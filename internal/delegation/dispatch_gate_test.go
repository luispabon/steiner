package delegation

import (
	"context"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

// followerReleaseDelay is long enough for the follower to be blocked in its
// wait when the test releases the gate, cancels the context, or lets a
// shortened dispatch gate timeout fire.
const followerReleaseDelay = 20 * time.Millisecond

func TestApplyDispatchGateFollowerEmitsRelease(t *testing.T) {
	const cacheKey = "gate-key"
	occ := output.DelegationOccurrence{CallID: "call-1", BatchID: "batch-1", AgentID: "child-follower"}
	tests := []struct {
		name        string
		timeout     time.Duration
		cancel      bool
		wantReason  output.DelegationCacheReleaseReason
		minWaitedMs int64
	}{
		{name: "leader first response", wantReason: output.DelegationCacheReleaseLeaderFirstResponse, minWaitedMs: 10},
		{name: "timeout", timeout: followerReleaseDelay, wantReason: output.DelegationCacheReleaseTimeout, minWaitedMs: 15},
		{name: "context cancelled", cancel: true, wantReason: output.DelegationCacheReleaseCancelled, minWaitedMs: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := NewCacheKeyStore()
			store.testWaitTimeout = tt.timeout
			leader, release, _ := store.BeginDispatch(cacheKey)
			if !leader {
				t.Fatal("first BeginDispatch() isLeader = false, want true")
			}
			t.Cleanup(release)
			events := &recordingEventSink{}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			go func() {
				time.Sleep(followerReleaseDelay)
				if tt.cancel {
					cancel()
					return
				}
				release()
			}()

			start := time.Now()
			sink, followerRelease := applyDispatchGate(ctx, store, cacheKey, occ, events, output.NoopSink{})
			followerRelease()
			elapsedMs := time.Since(start).Milliseconds()
			if sink == nil {
				t.Fatal("applyDispatchGate() sink = nil, want the child sink")
			}

			got := events.Events()
			if len(got) != 2 {
				t.Fatalf("emitted %d events, want waiting then released", len(got))
			}
			if got[0].Type != output.EventTypeDelegationCacheWaiting || got[1].Type != output.EventTypeDelegationCacheReleased {
				t.Fatalf("event types = %q, %q, want %q, %q", got[0].Type, got[1].Type, output.EventTypeDelegationCacheWaiting, output.EventTypeDelegationCacheReleased)
			}
			payload, ok := got[1].Payload.(output.DelegationCacheReleasedEvent)
			if !ok {
				t.Fatalf("released payload = %T, want output.DelegationCacheReleasedEvent", got[1].Payload)
			}
			if payload.DelegationOccurrence != occ {
				t.Errorf("released occurrence = %+v, want %+v", payload.DelegationOccurrence, occ)
			}
			if payload.Reason != tt.wantReason {
				t.Errorf("released reason = %q, want %q", payload.Reason, tt.wantReason)
			}
			if payload.WaitedMs < tt.minWaitedMs {
				t.Errorf("waited_ms = %d, want at least %d", payload.WaitedMs, tt.minWaitedMs)
			}
			if payload.WaitedMs > elapsedMs {
				t.Errorf("waited_ms = %d exceeds the %d ms spent in applyDispatchGate", payload.WaitedMs, elapsedMs)
			}
		})
	}
}

func TestApplyDispatchGateLeaderEmitsNoRelease(t *testing.T) {
	t.Parallel()
	store := NewCacheKeyStore()
	parentEvents := &recordingEventSink{}
	childEvents := &recordingEventSink{}
	sink, release := applyDispatchGate(context.Background(), store, "leader-key", output.DelegationOccurrence{CallID: "call-1", AgentID: "child-leader"}, parentEvents, childEvents)
	t.Cleanup(release)

	sink.Emit(output.Event{Type: output.EventTypeAPIResponse})

	if got := parentEvents.Events(); len(got) != 0 {
		t.Fatalf("leader emitted %d parent events, want none", len(got))
	}
	if got := childEvents.Events(); len(got) != 1 || got[0].Type != output.EventTypeAPIResponse {
		t.Fatalf("child events = %v, want the forwarded api response", got)
	}
}
