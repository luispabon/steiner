package delegation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

// gateTestTimeout is a shortened dispatch gate timeout: long enough that clock
// and scheduler noise cannot turn a released wait into a timeout, short enough
// to keep the test fast.
const gateTestTimeout = 20 * time.Millisecond

// gateWaitSignalSink records events and closes waiting once the follower's
// cache-waiting event arrives, so tests act only after the follower has joined
// the gate. Every release path decides the release cause, so acting after this
// point keeps the cause deterministic regardless of scheduling.
type gateWaitSignalSink struct {
	*recordingEventSink
	waiting chan struct{}
	once    sync.Once
}

func newGateWaitSignalSink() *gateWaitSignalSink {
	return &gateWaitSignalSink{recordingEventSink: &recordingEventSink{}, waiting: make(chan struct{})}
}

func (s *gateWaitSignalSink) Emit(event output.Event) {
	s.recordingEventSink.Emit(event)
	if event.Type == output.EventTypeDelegationCacheWaiting {
		s.once.Do(func() { close(s.waiting) })
	}
}

// releasedEvent asserts the follower emitted exactly a waiting event followed by
// a release event, and returns the release payload.
func releasedEvent(t *testing.T, events []output.Event) output.DelegationCacheReleasedEvent {
	t.Helper()
	if len(events) != 2 {
		t.Fatalf("emitted %d events, want waiting then released", len(events))
	}
	if events[0].Type != output.EventTypeDelegationCacheWaiting || events[1].Type != output.EventTypeDelegationCacheReleased {
		t.Fatalf("event types = %q, %q, want %q, %q", events[0].Type, events[1].Type, output.EventTypeDelegationCacheWaiting, output.EventTypeDelegationCacheReleased)
	}
	payload, ok := events[1].Payload.(output.DelegationCacheReleasedEvent)
	if !ok {
		t.Fatalf("released payload = %T, want output.DelegationCacheReleasedEvent", events[1].Payload)
	}
	return payload
}

func TestApplyDispatchGateFollowerEmitsRelease(t *testing.T) {
	const cacheKey = "gate-key"
	occ := output.DelegationOccurrence{CallID: "call-1", BatchID: "batch-1", AgentID: "child-follower"}
	tests := []struct {
		name string
		// timeout shortens the gate wait; no case releases the gate itself, so
		// the timeout case ends on the timer alone.
		timeout time.Duration
		// firstResponse releases the gate through the leader's release sink, as
		// the leader's first provider response does.
		firstResponse bool
		cancel        bool
		wantReason    output.DelegationCacheReleaseReason
		minWaitedMs   int64
	}{
		{name: "leader first response", firstResponse: true, wantReason: output.DelegationCacheReleaseLeaderFirstResponse},
		{name: "timeout", timeout: gateTestTimeout, wantReason: output.DelegationCacheReleaseTimeout, minWaitedMs: 15},
		{name: "context cancelled", cancel: true, wantReason: output.DelegationCacheReleaseCancelled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := NewCacheKeyStore()
			store.testWaitTimeout = tt.timeout
			leaderOcc := output.DelegationOccurrence{CallID: "call-0", BatchID: "batch-1", AgentID: "child-leader"}
			leaderSink, leaderTeardown := applyDispatchGate(context.Background(), store, cacheKey, leaderOcc, &recordingEventSink{}, &recordingEventSink{})
			// The leader outlives every case here: releasing it in cleanup keeps
			// only the case's own release path in play while the follower waits.
			t.Cleanup(leaderTeardown)

			events := newGateWaitSignalSink()
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			if tt.firstResponse || tt.cancel {
				go func() {
					<-events.waiting
					if tt.cancel {
						cancel()
						return
					}
					leaderSink.Emit(output.Event{Type: output.EventTypeAPIResponse})
				}()
			}

			start := time.Now()
			_, followerRelease := applyDispatchGate(ctx, store, cacheKey, occ, events, output.NoopSink{})
			followerRelease()
			elapsedMs := time.Since(start).Milliseconds()

			payload := releasedEvent(t, events.Events())
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

func TestApplyDispatchGateLeaderReleaseCause(t *testing.T) {
	const cacheKey = "cause-key"
	followerOcc := output.DelegationOccurrence{CallID: "call-2", BatchID: "batch-1", AgentID: "child-follower"}
	tests := []struct {
		name    string
		release func(firstResponse, teardown func())
		want    output.DelegationCacheReleaseReason
	}{
		{
			name:    "first api response via release sink",
			release: func(firstResponse, _ func()) { firstResponse() },
			want:    output.DelegationCacheReleaseLeaderFirstResponse,
		},
		{
			name:    "leader teardown without a response",
			release: func(_, teardown func()) { teardown() },
			want:    output.DelegationCacheReleaseLeaderExited,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := NewCacheKeyStore()
			leaderEvents := &recordingEventSink{}
			leaderOcc := output.DelegationOccurrence{CallID: "call-1", BatchID: "batch-1", AgentID: "child-leader"}
			leaderSink, leaderTeardown := applyDispatchGate(context.Background(), store, cacheKey, leaderOcc, leaderEvents, &recordingEventSink{})
			t.Cleanup(leaderTeardown)

			events := newGateWaitSignalSink()
			go func() {
				<-events.waiting
				firstResponse := func() { leaderSink.Emit(output.Event{Type: output.EventTypeAPIResponse}) }
				tt.release(firstResponse, leaderTeardown)
			}()

			start := time.Now()
			_, followerRelease := applyDispatchGate(context.Background(), store, cacheKey, followerOcc, events, output.NoopSink{})
			followerRelease()
			elapsedMs := time.Since(start).Milliseconds()

			payload := releasedEvent(t, events.Events())
			if payload.Reason != tt.want {
				t.Errorf("released reason = %q, want %q", payload.Reason, tt.want)
			}
			if payload.WaitedMs < 0 || payload.WaitedMs > elapsedMs {
				t.Errorf("waited_ms = %d, want between 0 and the %d ms spent in applyDispatchGate", payload.WaitedMs, elapsedMs)
			}
			if got := leaderEvents.Events(); len(got) != 0 {
				t.Errorf("leader emitted %d events, want none", len(got))
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

// TestApplyDispatchGateSparseSinks covers the degenerate sink inputs: a nil store
// dispatches ungated, and a follower whose parent has no event sink waits and
// releases without emitting anything. Both must pass the child sink through
// unwrapped and must not panic.
func TestApplyDispatchGateSparseSinks(t *testing.T) {
	const cacheKey = "sparse-key"
	followerOcc := output.DelegationOccurrence{CallID: "call-2", BatchID: "batch-1", AgentID: "child-follower"}
	leaderOcc := output.DelegationOccurrence{CallID: "call-1", BatchID: "batch-1", AgentID: "child-leader"}
	tests := []struct {
		name      string
		store     func(t *testing.T) *CacheKeyStore
		nilParent bool
	}{
		{
			name:  "nil store",
			store: func(*testing.T) *CacheKeyStore { return nil },
		},
		{
			name: "follower with nil parent events",
			store: func(t *testing.T) *CacheKeyStore {
				store := NewCacheKeyStore()
				store.testWaitTimeout = gateTestTimeout
				_, leaderRelease := applyDispatchGate(context.Background(), store, cacheKey, leaderOcc, output.NoopSink{}, output.NoopSink{})
				t.Cleanup(leaderRelease)
				return store
			},
			nilParent: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			childEvents := &recordingEventSink{}
			var parentEvents output.EventSink
			if !tt.nilParent {
				parentEvents = &recordingEventSink{}
			}

			sink, release := applyDispatchGate(context.Background(), tt.store(t), cacheKey, followerOcc, parentEvents, childEvents)
			t.Cleanup(release)

			if sink != output.EventSink(childEvents) {
				t.Fatal("applyDispatchGate returned a wrapped sink, want the child sink passed through")
			}
			sink.Emit(output.Event{Type: output.EventTypeAPIResponse})
			if got := childEvents.Events(); len(got) != 1 || got[0].Type != output.EventTypeAPIResponse {
				t.Fatalf("child events = %v, want the forwarded api response", got)
			}
			if recorder, ok := parentEvents.(*recordingEventSink); ok {
				if got := recorder.Events(); len(got) != 0 {
					t.Fatalf("parent events = %v, want none", got)
				}
			}
		})
	}
}
