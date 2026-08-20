package delegation

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCacheKeyStoreKeyForReusesSameAgentType(t *testing.T) {
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

func TestCacheKeyStoreBeginDispatchLeaderAndFollower(t *testing.T) {
	store := NewCacheKeyStore()
	isLeader, release, _ := store.BeginDispatch("shared-key")
	if !isLeader {
		t.Fatal("first BeginDispatch() isLeader = false, want true")
	}

	followerLeader, _, wait := store.BeginDispatch("shared-key")
	if followerLeader {
		t.Fatal("second BeginDispatch() isLeader = true, want false")
	}

	waitDone := make(chan struct{})
	go func() {
		wait(context.Background())
		close(waitDone)
	}()
	select {
	case <-waitDone:
		t.Fatal("follower wait returned before leader release")
	case <-time.After(20 * time.Millisecond):
	}

	release()
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("follower wait did not return after leader release")
	}
}

func TestCacheKeyStoreBeginDispatchWaitCancellation(t *testing.T) {
	store := NewCacheKeyStore()
	_, _, _ = store.BeginDispatch("cancel-key")
	_, _, wait := store.BeginDispatch("cancel-key")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		wait(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("follower wait did not return after context cancellation")
	}
}

func TestCacheKeyStoreBeginDispatchReleaseStartsNewWave(t *testing.T) {
	store := NewCacheKeyStore()
	firstLeader, firstRelease, _ := store.BeginDispatch("wave-key")
	if !firstLeader {
		t.Fatal("first BeginDispatch() isLeader = false, want true")
	}
	firstRelease()

	secondLeader, secondRelease, _ := store.BeginDispatch("wave-key")
	if !secondLeader {
		t.Fatal("new wave BeginDispatch() isLeader = false, want true")
	}
	followerLeader, _, secondWait := store.BeginDispatch("wave-key")
	if followerLeader {
		t.Fatal("second wave follower BeginDispatch() isLeader = true, want false")
	}

	firstRelease()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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

	secondRelease()
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("new wave wait did not return after release")
	}
}

func TestCacheKeyStoreBeginDispatchEmptyKeyIsUngated(t *testing.T) {
	store := NewCacheKeyStore()
	_, release, _ := store.BeginDispatch("existing-key")
	isLeader, emptyRelease, wait := store.BeginDispatch("")
	if !isLeader {
		t.Fatal("empty-key BeginDispatch() isLeader = false, want true")
	}
	emptyRelease()
	wait(context.Background())
	release()

	isLeader, _, _ = store.BeginDispatch("")
	if !isLeader {
		t.Fatal("empty-key BeginDispatch() after release isLeader = false, want true")
	}
}

func TestCacheKeyStoreDispatchGateTimeout(t *testing.T) {
	if dispatchGateTimeout != 10*time.Second {
		t.Fatalf("dispatchGateTimeout = %s, want 10s", dispatchGateTimeout)
	}

	gate := &dispatchGate{ready: make(chan struct{})}
	waitDone := make(chan struct{})
	go func() {
		waitForTimeout(gate, 20*time.Millisecond)(context.Background())
		close(waitDone)
	}()

	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("waitForTimeout did not return after its timeout")
	}
}
