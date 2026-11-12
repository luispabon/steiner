package interactive

import (
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

func TestActiveRunControllerSteerQueue(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "send and receive via SteerQueue().Drain",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				c.SteerQueue().Add(agent.SteerMessage{Text: "hello"})
				got := c.SteerQueue().Drain()
				if len(got) != 1 || got[0].Text != "hello" {
					t.Errorf("Drain() = %+v, want [{hello nil}]", got)
				}
			},
		},
		{
			name: "Drain returns nil when no message pending",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				got := c.SteerQueue().Drain()
				if len(got) != 0 {
					t.Errorf("Drain() = %+v, want empty", got)
				}
			},
		},
		{
			name: "FIFO ordering: Drain returns all steers in order",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				c.SteerQueue().Add(agent.SteerMessage{Text: "first"})
				c.SteerQueue().Add(agent.SteerMessage{Text: "second"})
				c.SteerQueue().Add(agent.SteerMessage{Text: "third"})
				got := c.SteerQueue().Drain()
				if len(got) != 3 {
					t.Fatalf("Drain() = %+v, want 3 items", got)
				}
				if got[0].Text != "first" || got[1].Text != "second" || got[2].Text != "third" {
					t.Errorf("Drain() = %+v, want [first second third]", got)
				}
			},
		},
		{
			name: "Add with images preserves them in Drain",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				imgs := []agent.ImageBlock{{MediaType: "image/png", Data: "abc"}}
				c.SteerQueue().Add(agent.SteerMessage{Text: "see image", Images: imgs})
				got := c.SteerQueue().Drain()
				if len(got) != 1 {
					t.Fatalf("Drain() = %+v, want 1 item", got)
				}
				if got[0].Text != "see image" {
					t.Errorf("Text = %q, want %q", got[0].Text, "see image")
				}
				if len(got[0].Images) != 1 || got[0].Images[0].MediaType != "image/png" {
					t.Errorf("Images = %+v, want 1 png image", got[0].Images)
				}
			},
		},
		{
			name: "Drain empties queue on second call",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				c.SteerQueue().Add(agent.SteerMessage{Text: "msg"})
				c.SteerQueue().Drain()
				got := c.SteerQueue().Drain()
				if len(got) != 0 {
					t.Errorf("second Drain() = %+v, want empty", got)
				}
			},
		},
		{
			name: "Release preserves the queue and does not clear a successor",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				first := c.Set(func() {})
				c.SteerQueue().Add(agent.SteerMessage{Text: "pending"})
				second := c.Set(func() {})

				c.Release(first)
				if !c.HasCancel() {
					t.Fatal("stale Release cleared successor cancel function")
				}
				c.Release(second)
				if c.HasCancel() {
					t.Fatal("Release did not clear current cancel function")
				}
				got := c.SteerQueue().Snapshot()
				if len(got) != 1 || got[0].Text != "pending" {
					t.Errorf("after Release(), Snapshot() = %+v, want pending message", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.test)
	}
}

func TestApprovalCoordinatorQueue(t *testing.T) {
	coord := &ApprovalCoordinator{}
	headID, head := coord.Begin("head", "", "", "code")
	_, middle := coord.Begin("middle", "", "", "review")
	tailID, tail := coord.Begin("tail", "", "", "plan")
	if !coord.HasPending() || coord.PendingDepth() != 3 {
		t.Fatalf("pending state = %v/%d, want true/3", coord.HasPending(), coord.PendingDepth())
	}

	coord.Finish(middle)
	if coord.PendingDepth() != 2 {
		t.Fatalf("depth after middle Finish = %d, want 2", coord.PendingDepth())
	}
	coord.Submit(SubmitApproval{Identity: headID, Tool: "head", Decision: "allow_once"})
	if got := (<-head).Decision; got != "allow_once" {
		t.Errorf("head decision = %q, want allow_once", got)
	}
	select {
	case <-tail:
		t.Fatal("tail received submission before head was finished")
	default:
	}
	coord.Finish(head)
	coord.Submit(SubmitApproval{Identity: tailID, Tool: "tail", Decision: "deny"})
	if got := (<-tail).Decision; got != "deny" {
		t.Errorf("tail decision = %q, want deny", got)
	}
	coord.Finish(tail)
	if coord.HasPending() || coord.PendingDepth() != 0 {
		t.Fatalf("final pending state = %v/%d, want false/0", coord.HasPending(), coord.PendingDepth())
	}
}

// Snapshot returns the head and every registered identity from one view of the
// queue, so a terminal reset preserves all live controls, including claimed
// requests that have not unregistered yet.
func TestApprovalCoordinatorSnapshot(t *testing.T) {
	coord := &ApprovalCoordinator{}
	headID, head := coord.Begin("head", "", "", "code")
	middleID, middle := coord.Begin("middle", "", "", "review")
	tailID, tail := coord.Begin("tail", "", "", "plan")

	assertSnapshot := func(stage, wantHead string, wantIdentities ...string) {
		t.Helper()
		hend, identities := coord.Snapshot()
		if hend != wantHead {
			t.Fatalf("%s: head = %q, want %q", stage, hend, wantHead)
		}
		if len(identities) != len(wantIdentities) {
			t.Fatalf("%s: identities = %v, want %v", stage, identities, wantIdentities)
		}
		for i := range wantIdentities {
			if identities[i] != wantIdentities[i] {
				t.Fatalf("%s: identities = %v, want %v", stage, identities, wantIdentities)
			}
		}
	}
	assertSnapshot("all registered", headID, headID, middleID, tailID)

	coord.Submit(SubmitApproval{Identity: middleID, Tool: "middle", Decision: "allow_once"})
	assertSnapshot("claimed stays registered", headID, headID, middleID, tailID)

	// A snapshot is a copy: later registrations and finishes do not mutate it.
	snapshotHead, snapshot := coord.Snapshot()
	coord.Finish(middle)
	assertSnapshot("after middle Finish", headID, headID, tailID)
	if len(snapshot) != 3 {
		t.Fatalf("earlier snapshot identities = %v, want the three registrations it was taken with", snapshot)
	}
	if snapshotHead != headID {
		t.Fatalf("earlier snapshot head = %q, want %q", snapshotHead, headID)
	}

	// Claiming every remaining request leaves the queue registered but without
	// an unclaimed head; that is distinct from having no coordinator at all.
	coord.Submit(SubmitApproval{Identity: headID, Tool: "head", Decision: "allow_once"})
	coord.Submit(SubmitApproval{Identity: tailID, Tool: "tail", Decision: "deny"})
	assertSnapshot("every request claimed", "", headID, tailID)

	coord.Finish(head)
	coord.Finish(tail)
	assertSnapshot("after all Finish", "")
}

// A snapshot taken while requests register and finish must stay coherent: the
// head is empty or present in the identities, and identities never repeat.
func TestApprovalCoordinatorSnapshotConcurrentFinish(t *testing.T) {
	coord := &ApprovalCoordinator{}
	const rounds = 64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < rounds; i++ {
			_, ch := coord.Begin("write", "", "", "")
			coord.Finish(ch)
		}
	}()
	for i := 0; i < rounds; i++ {
		head, identities := coord.Snapshot()
		seen := make(map[string]bool)
		for _, identity := range identities {
			if seen[identity] {
				t.Fatalf("snapshot repeated identity %q", identity)
			}
			seen[identity] = true
		}
		if head != "" && !seen[head] {
			t.Fatalf("snapshot head %q missing from identities %v", head, identities)
		}
	}
	<-done
	if head, identities := coord.Snapshot(); head != "" || len(identities) != 0 {
		t.Fatalf("snapshot after every Finish = (%q, %v), want (\"\", none)", head, identities)
	}
}

func TestApprovalCoordinatorDuplicateSubmitClaimsOnlyHead(t *testing.T) {
	coord := &ApprovalCoordinator{}
	headID, head := coord.Begin("head", "", "", "")
	tailID, tail := coord.Begin("tail", "", "", "")

	coord.Submit(SubmitApproval{Identity: headID, Tool: "head", Decision: "first"})
	coord.Submit(SubmitApproval{Identity: headID, Tool: "head", Decision: "duplicate"})

	if got := (<-head).Decision; got != "first" {
		t.Fatalf("head decision = %q, want first", got)
	}
	select {
	case got := <-tail:
		t.Fatalf("tail received stale decision %q", got.Decision)
	default:
	}
	if got := coord.HeadIdentity(); got != tailID {
		t.Fatalf("head identity = %q, want %q", got, tailID)
	}
}

func TestApprovalCoordinatorDuplicateSameToolIdentityDoesNotAdvanceTail(t *testing.T) {
	coord := &ApprovalCoordinator{}
	firstID, first := coord.Begin("bash", "", "", "")
	secondID, second := coord.Begin("bash", "", "", "")

	coord.Submit(SubmitApproval{Identity: firstID, Tool: "bash", Decision: "allow_once"})
	if got := <-first; got.Decision != "allow_once" {
		t.Fatalf("first decision = %q, want allow_once", got.Decision)
	}
	for i := 0; i < 2; i++ {
		coord.Submit(SubmitApproval{Identity: firstID, Tool: "bash", Decision: "duplicate"})
	}
	select {
	case got := <-second:
		t.Fatalf("second received duplicate decision %q", got.Decision)
	default:
	}
	if got := coord.HeadIdentity(); got != secondID {
		t.Fatalf("head identity = %q, want %q", got, secondID)
	}
}

func TestApprovalCoordinatorConcurrentBeginAndSubmit(t *testing.T) {
	coord := &ApprovalCoordinator{}
	started := make(chan struct {
		name     string
		identity string
		ch       chan SubmitApproval
	}, 2)
	for _, name := range []string{"a", "b"} {
		n := name
		go func() {
			identity, ch := coord.Begin(n, "", "", "")
			started <- struct {
				name     string
				identity string
				ch       chan SubmitApproval
			}{name: n, identity: identity, ch: ch}
		}()
	}
	first := <-started
	second := <-started
	channelA, channelB := first, second
	if channelA.name != "a" {
		channelA, channelB = channelB, channelA
	}

	coord.Submit(SubmitApproval{Identity: channelA.identity, Decision: "first"})
	select {
	case got := <-channelA.ch:
		if got.Decision != "first" {
			t.Fatalf("channel a decision = %q, want first", got.Decision)
		}
	case <-time.After(time.Second):
		t.Fatal("first response timed out")
	}
	coord.Finish(channelA.ch)

	coord.Submit(SubmitApproval{Identity: channelB.identity, Decision: "second"})
	select {
	case got := <-channelB.ch:
		if got.Decision != "second" {
			t.Fatalf("channel b decision = %q, want second", got.Decision)
		}
	case <-time.After(time.Second):
		t.Fatal("second response timed out")
	}
	coord.Finish(channelB.ch)
}

func TestApprovalCoordinatorSubmitFinishRace(t *testing.T) {
	coord := &ApprovalCoordinator{}
	_, a := coord.Begin("a", "", "", "")
	bID, b := coord.Begin("b", "", "", "")
	finishStarted := make(chan struct{})
	finishDone := make(chan struct{})
	go func() {
		close(finishStarted)
		coord.Finish(a)
		close(finishDone)
	}()
	<-finishStarted
	coord.Submit(SubmitApproval{Identity: bID, Tool: "b", Decision: "allow_once"})
	<-finishDone
	select {
	case got := <-b:
		if got.Decision != "allow_once" {
			t.Fatalf("b decision = %q", got.Decision)
		}
	case <-time.After(time.Second):
		t.Fatal("b did not receive submission")
	}
	select {
	case <-a:
		t.Fatal("removed a received submission")
	default:
	}
}
