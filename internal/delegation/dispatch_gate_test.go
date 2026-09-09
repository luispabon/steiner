package delegation

import (
	"context"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

func TestApplyDispatchGateDeferredReleaseUnblocksFollower(t *testing.T) {
	store := NewCacheKeyStore()
	_, leaderRelease := applyDispatchGate(context.Background(), store, "shared", "leader", "call", output.NoopSink{}, output.NoopSink{})

	followerWaiting := make(chan struct{})
	followerDone := make(chan struct{})
	parentEvents := output.SinkFunc(func(event output.Event) {
		if event.Type == output.EventTypeDelegationCacheWaiting {
			close(followerWaiting)
		}
	})
	go func() {
		_, followerRelease := applyDispatchGate(context.Background(), store, "shared", "follower", "call", parentEvents, output.NoopSink{})
		defer followerRelease()
		close(followerDone)
	}()

	select {
	case <-followerWaiting:
	case <-time.After(time.Second):
		t.Fatal("follower did not enter dispatch gate")
	}
	select {
	case <-followerDone:
		t.Fatal("follower unblocked before leader exit")
	default:
	}

	func() {
		defer leaderRelease()
		// Leader exits without emitting APIResponse. Deferred gateRelease is the
		// safety behavior under test.
	}()

	select {
	case <-followerDone:
	case <-time.After(time.Second):
		t.Fatal("deferred leader release did not unblock follower")
	}
}
