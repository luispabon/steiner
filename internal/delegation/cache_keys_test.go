package delegation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

func TestCacheKeyStoreKeyForReusesSameAgentType(t *testing.T) {
	t.Parallel()
	store := NewCacheKeyStore()
	n := 0
	mint := func() (string, error) {
		n++
		return "key-" + string(rune('a'+n-1)), nil
	}

	first, err := store.KeyFor(AgentTypeCode, mint)
	if err != nil {
		t.Fatalf("KeyFor() error = %v", err)
	}
	second, err := store.KeyFor(AgentTypeCode, mint)
	if err != nil {
		t.Fatalf("KeyFor() error = %v", err)
	}
	if first != second {
		t.Errorf("KeyFor() second call = %q, want reused %q", second, first)
	}
	if n != 1 {
		t.Errorf("mint called %d times, want 1 (only first call should mint)", n)
	}
}

func TestCacheKeyStoreKeyForDiffersByAgentType(t *testing.T) {
	t.Parallel()
	store := NewCacheKeyStore()
	n := 0
	mint := func() (string, error) {
		n++
		return "key-" + string(rune('a'+n-1)), nil
	}

	codeKey, err := store.KeyFor(AgentTypeCode, mint)
	if err != nil {
		t.Fatalf("KeyFor() error = %v", err)
	}
	reviewKey, err := store.KeyFor(AgentTypeReview, mint)
	if err != nil {
		t.Fatalf("KeyFor() error = %v", err)
	}
	if codeKey == reviewKey {
		t.Errorf("KeyFor() for different agent types returned the same key %q", codeKey)
	}
}

func TestCacheKeyStoreKeyForMintErrorNotCached(t *testing.T) {
	t.Parallel()
	store := NewCacheKeyStore()
	wantErr := errors.New("mint failed")
	n := 0
	mint := func() (string, error) {
		n++
		if n == 1 {
			return "", wantErr
		}
		return "recovered-key", nil
	}

	_, err := store.KeyFor(AgentTypeExplore, mint)
	if !errors.Is(err, wantErr) {
		t.Fatalf("KeyFor() error = %v, want %v", err, wantErr)
	}

	key, err := store.KeyFor(AgentTypeExplore, mint)
	if err != nil {
		t.Fatalf("KeyFor() second call error = %v", err)
	}
	if key != "recovered-key" {
		t.Errorf("KeyFor() second call = %q, want %q (retry after error, not cached failure)", key, "recovered-key")
	}
	if n != 2 {
		t.Errorf("mint called %d times, want 2 (error must not be cached)", n)
	}
}

func TestCacheKeyStoreDispatchGateLeaderAndFollower(t *testing.T) {
	t.Parallel()
	store := NewCacheKeyStore()
	isLeader, release, _ := store.beginDispatch("shared-key")
	t.Cleanup(func() { release(output.DelegationCacheReleaseLeaderExited) })
	if !isLeader {
		t.Fatal("first beginDispatch() isLeader = false, want true")
	}

	followerLeader, _, wait := store.beginDispatch("shared-key")
	if followerLeader {
		t.Fatal("second beginDispatch() isLeader = true, want false")
	}

	// A cancellable context plus the registered release cleanup guarantees the
	// follower goroutine unblocks even if a fatal assertion exits the test
	// before the explicit release below.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	waitDone := make(chan struct{})
	go func() {
		wait(ctx)
		close(waitDone)
	}()
	select {
	case <-waitDone:
		t.Fatal("follower wait returned before leader release")
	case <-time.After(20 * time.Millisecond):
	}

	release(output.DelegationCacheReleaseLeaderExited)
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("follower wait did not return after leader release")
	}
}

// waitReason runs a gate wait under a one-second bound, so a regression in the
// gate's timer or cancellation handling fails the test instead of hanging it.
// The channel is buffered so a late wait return does not leak the goroutine.
func waitReason(t *testing.T, wait func() output.DelegationCacheReleaseReason) output.DelegationCacheReleaseReason {
	t.Helper()
	reasons := make(chan output.DelegationCacheReleaseReason, 1)
	go func() { reasons <- wait() }()
	select {
	case reason := <-reasons:
		return reason
	case <-time.After(time.Second):
		t.Fatal("gate wait did not return within a second")
		return ""
	}
}

func TestCacheKeyStoreDispatchGateWaitCancellation(t *testing.T) {
	t.Parallel()
	store := NewCacheKeyStore()
	_, release, _ := store.beginDispatch("cancel-key")
	t.Cleanup(func() { release(output.DelegationCacheReleaseLeaderExited) })
	_, _, wait := store.beginDispatch("cancel-key")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if reason := waitReason(t, func() output.DelegationCacheReleaseReason { return wait(ctx) }); reason != output.DelegationCacheReleaseCancelled {
		t.Fatalf("wait reason = %q, want %q", reason, output.DelegationCacheReleaseCancelled)
	}
}

func TestCacheKeyStoreDispatchGateReleaseStartsNewWave(t *testing.T) {
	t.Parallel()
	store := NewCacheKeyStore()
	firstLeader, firstRelease, _ := store.beginDispatch("wave-key")
	t.Cleanup(func() { firstRelease(output.DelegationCacheReleaseLeaderExited) })
	if !firstLeader {
		t.Fatal("first beginDispatch() isLeader = false, want true")
	}
	firstRelease(output.DelegationCacheReleaseLeaderExited)

	secondLeader, secondRelease, _ := store.beginDispatch("wave-key")
	t.Cleanup(func() { secondRelease(output.DelegationCacheReleaseLeaderExited) })
	if !secondLeader {
		t.Fatal("new wave beginDispatch() isLeader = false, want true")
	}
	followerLeader, _, secondWait := store.beginDispatch("wave-key")
	if followerLeader {
		t.Fatal("second wave follower beginDispatch() isLeader = true, want false")
	}

	firstRelease(output.DelegationCacheReleaseLeaderExited)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	waitDone := make(chan struct{})
	go func() {
		secondWait(ctx)
		close(waitDone)
	}()
	select {
	case <-waitDone:
		t.Fatal("new wave wait returned after old leader release")
	case <-time.After(20 * time.Millisecond):
	}

	secondRelease(output.DelegationCacheReleaseLeaderExited)
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("new wave wait did not return after release")
	}
}

func TestCacheKeyStoreDispatchGateEmptyKeyIsUngated(t *testing.T) {
	t.Parallel()
	store := NewCacheKeyStore()
	_, release, _ := store.beginDispatch("existing-key")
	isLeader, emptyRelease, wait := store.beginDispatch("")
	if !isLeader {
		t.Fatal("empty-key beginDispatch() isLeader = false, want true")
	}
	emptyRelease(output.DelegationCacheReleaseLeaderExited)
	wait(context.Background())
	release(output.DelegationCacheReleaseLeaderExited)

	isLeader, _, _ = store.beginDispatch("")
	if !isLeader {
		t.Fatal("empty-key beginDispatch() after release isLeader = false, want true")
	}
}

func TestCacheKeyStoreDispatchGateTimeout(t *testing.T) {
	t.Parallel()
	if dispatchGateTimeout != 10*time.Second {
		t.Fatalf("dispatchGateTimeout = %s, want 10s", dispatchGateTimeout)
	}

	gate := &dispatchGate{ready: make(chan struct{})}
	defer gate.release(output.DelegationCacheReleaseLeaderExited)
	wait := func() output.DelegationCacheReleaseReason {
		return waitForTimeout(gate, 20*time.Millisecond)(context.Background())
	}
	if reason := waitReason(t, wait); reason != output.DelegationCacheReleaseTimeout {
		t.Fatalf("waitForTimeout reason = %q, want %q", reason, output.DelegationCacheReleaseTimeout)
	}
}

// TestAwaitReportsReleaseCause pins that a gate released at the same instant the
// timer or the follower's context ends reports the release cause. The select
// randomises between the two ready cases, so the loop hits both orderings.
func TestAwaitReportsReleaseCause(t *testing.T) {
	t.Parallel()
	expired := make(chan time.Time)
	close(expired)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name    string
		expired <-chan time.Time
		ctx     context.Context
	}{
		{name: "timer fired", expired: expired, ctx: context.Background()},
		{name: "context cancelled", expired: make(chan time.Time), ctx: cancelled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gate := &dispatchGate{ready: make(chan struct{})}
			gate.release(output.DelegationCacheReleaseLeaderFirstResponse)

			for i := 0; i < 100; i++ {
				if reason := gate.await(tt.ctx, tt.expired); reason != output.DelegationCacheReleaseLeaderFirstResponse {
					t.Fatalf("iteration %d: wait reason = %q, want %q", i, reason, output.DelegationCacheReleaseLeaderFirstResponse)
				}
			}
		})
	}
}
