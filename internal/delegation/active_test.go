package delegation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestActiveDelegateCancellationIsolation(t *testing.T) {
	controller := NewActiveController()
	parent := context.Background()
	alphaWorktree := CodeWorktree{Path: "/tmp/alpha", Branch: "delegate/alpha"}
	alpha, err := controller.Register("alpha", parent, AgentTypeCode, alphaWorktree)
	if err != nil {
		t.Fatalf("Register(alpha) returned error: %v", err)
	}
	beta, err := controller.Register("beta", parent, AgentTypeExplore, CodeWorktree{})
	if err != nil {
		t.Fatalf("Register(beta) returned error: %v", err)
	}

	if got, want := controller.ActiveAgentIDs(), []string{"alpha", "beta"}; !equalStrings(got, want) {
		t.Fatalf("ActiveAgentIDs() = %v, want %v", got, want)
	}
	if got, ok := controller.WorktreeFor("alpha"); !ok || got != alphaWorktree {
		t.Fatalf("WorktreeFor(alpha) = %#v, %t, want %#v, true", got, ok, alphaWorktree)
	}
	if got, ok := controller.TypeFor("alpha"); !ok || got != AgentTypeCode {
		t.Fatalf("TypeFor(alpha) = %q, %t, want %q, true", got, ok, AgentTypeCode)
	}

	if !controller.CancelAgent("alpha") {
		t.Fatal("CancelAgent(alpha) returned false, want true")
	}
	assertContextDone(alpha, t, "alpha after CancelAgent")
	assertContextLive(beta, t, "beta after alpha cancellation")

	if controller.CancelAgent("missing") {
		t.Fatal("CancelAgent(missing) returned true, want false")
	}
	assertContextLive(parent, t, "parent after alpha cancellation")

	if !controller.CancelAgent("alpha") {
		t.Fatal("CancelAgent(alpha) after cancellation returned false, want true")
	}
	if _, ok := controller.WorktreeFor("alpha"); !ok {
		t.Fatal("WorktreeFor(alpha) after cancellation returned false, want true")
	}
	if _, ok := controller.TypeFor("alpha"); !ok {
		t.Fatal("TypeFor(alpha) after cancellation returned false, want true")
	}

	controller.Unregister("alpha")
	controller.Unregister("missing")
	if got, want := controller.ActiveAgentIDs(), []string{"beta"}; !equalStrings(got, want) {
		t.Fatalf("ActiveAgentIDs() after Unregister = %v, want %v", got, want)
	}
	if _, ok := controller.WorktreeFor("alpha"); ok {
		t.Fatal("WorktreeFor(alpha) after Unregister returned true, want false")
	}
	if _, ok := controller.TypeFor("alpha"); ok {
		t.Fatal("TypeFor(alpha) after Unregister returned true, want false")
	}
}

func TestActiveDelegateCancelAllKeepsMetadata(t *testing.T) {
	controller := NewActiveController()
	contexts := make(map[string]context.Context)
	worktrees := map[string]CodeWorktree{
		"code":   {Path: "/tmp/code", Branch: "delegate/code"},
		"review": {},
	}
	types := map[string]AgentType{
		"code":   AgentTypeCode,
		"review": AgentTypeReview,
	}
	for _, agentID := range []string{"code", "review"} {
		ctx, err := controller.Register(agentID, context.Background(), types[agentID], worktrees[agentID])
		if err != nil {
			t.Fatalf("Register(%q) returned error: %v", agentID, err)
		}
		contexts[agentID] = ctx
	}

	controller.CancelAll()
	for _, agentID := range []string{"code", "review"} {
		assertContextDone(contexts[agentID], t, agentID+" after CancelAll")
		if got, ok := controller.WorktreeFor(agentID); !ok || got != worktrees[agentID] {
			t.Fatalf("WorktreeFor(%q) = %#v, %t after CancelAll, want %#v, true", agentID, got, ok, worktrees[agentID])
		}
		if got, ok := controller.TypeFor(agentID); !ok || got != types[agentID] {
			t.Fatalf("TypeFor(%q) = %q, %t after CancelAll, want %q, true", agentID, got, ok, types[agentID])
		}
	}
	if got, want := controller.ActiveAgentIDs(), []string{"code", "review"}; !equalStrings(got, want) {
		t.Fatalf("ActiveAgentIDs() after CancelAll = %v, want %v", got, want)
	}
}

func TestActiveDelegateDuplicateConcurrentRegistration(t *testing.T) {
	controller := NewActiveController()
	const attempts = 32

	contexts := make(chan context.Context, attempts)
	errorsSeen := make(chan error, attempts)
	var wg sync.WaitGroup
	wg.Add(attempts)
	for range attempts {
		go func() {
			defer wg.Done()
			ctx, err := controller.Register("same", context.Background(), AgentTypeExplore, CodeWorktree{})
			if err != nil {
				errorsSeen <- err
				return
			}
			contexts <- ctx
		}()
	}
	wg.Wait()
	close(contexts)
	close(errorsSeen)

	if got := len(contexts); got != 1 {
		t.Fatalf("successful registrations = %d, want 1", got)
	}
	for err := range errorsSeen {
		if !errors.Is(err, ErrAgentAlreadyActive) {
			t.Errorf("duplicate registration error = %v, want ErrAgentAlreadyActive", err)
		}
	}
	if got, want := controller.ActiveAgentIDs(), []string{"same"}; !equalStrings(got, want) {
		t.Fatalf("ActiveAgentIDs() = %v, want %v", got, want)
	}
	controller.CancelAgent("same")
	if ctx := <-contexts; !errors.Is(context.Cause(ctx), context.Canceled) {
		t.Fatalf("registered context cause = %v, want %v", context.Cause(ctx), context.Canceled)
	}
}

func TestActiveDelegateConcurrentRegisterAndCancel(t *testing.T) {
	controller := NewActiveController()
	const agentCount = 24
	contexts := make([]context.Context, agentCount)
	var registerWG sync.WaitGroup
	registerWG.Add(agentCount)
	for i := range agentCount {
		go func(i int) {
			defer registerWG.Done()
			ctx, err := controller.Register(fmt.Sprintf("agent-%d", i), context.Background(), AgentTypeExplore, CodeWorktree{})
			if err != nil {
				t.Errorf("Register(agent-%d) returned error: %v", i, err)
				return
			}
			contexts[i] = ctx
		}(i)
	}
	registerWG.Wait()

	var cancelWG sync.WaitGroup
	cancelWG.Add(agentCount)
	for i := range agentCount {
		go func(i int) {
			defer cancelWG.Done()
			if !controller.CancelAgent(fmt.Sprintf("agent-%d", i)) {
				t.Errorf("CancelAgent(agent-%d) returned false", i)
			}
		}(i)
	}
	cancelWG.Wait()

	for i, ctx := range contexts {
		assertContextDone(ctx, t, fmt.Sprintf("agent-%d after concurrent cancellation", i))
	}
	if got := len(controller.ActiveAgentIDs()); got != agentCount {
		t.Fatalf("active agent count after cancellation = %d, want %d", got, agentCount)
	}
}

func assertContextDone(ctx context.Context, t *testing.T, name string) {
	t.Helper()
	select {
	case <-ctx.Done():
	default:
		t.Fatalf("%s context is not cancelled", name)
	}
}

func assertContextLive(ctx context.Context, t *testing.T, name string) {
	t.Helper()
	select {
	case <-ctx.Done():
		t.Fatalf("%s context is cancelled", name)
	default:
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
