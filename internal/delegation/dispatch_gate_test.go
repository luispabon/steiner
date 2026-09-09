package delegation

import (
	"context"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

func TestApplyDispatchGateDeferredReleaseUnblocksFollower(t *testing.T) {
	store := NewCacheKeyStore()
	_, release, _ := store.BeginDispatch("shared")

	followerDone := make(chan struct{})
	go func() {
		_, gateRelease := applyDispatchGate(context.Background(), store, "shared", "follower", "call", output.NoopSink{}, output.NoopSink{})
		defer gateRelease()
		close(followerDone)
	}()

	select {
	case <-followerDone:
		t.Fatal("follower unblocked before leader exit")
	case <-time.After(25 * time.Millisecond):
	}

	leaderExit := func() {
		defer release()
		// Leader exits without emitting APIResponse.
	}
	leaderExit()

	select {
	case <-followerDone:
	case <-time.After(time.Second):
		t.Fatal("deferred gate release did not unblock follower")
	}
}
