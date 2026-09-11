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
			name: "Clear empties the queue",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				c.SteerQueue().Add(agent.SteerMessage{Text: "pending"})
				c.Clear()
				got := c.SteerQueue().Drain()
				if len(got) != 0 {
					t.Errorf("after Clear(), Drain() = %+v, want empty", got)
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
	head := coord.Begin("head", "head", "", "", "code")
	middle := coord.Begin("middle", "middle", "", "", "review")
	tail := coord.Begin("tail", "tail", "", "", "plan")
	if !coord.HasPending() || coord.PendingDepth() != 3 {
		t.Fatalf("pending state = %v/%d, want true/3", coord.HasPending(), coord.PendingDepth())
	}

	coord.Finish(middle)
	if coord.PendingDepth() != 2 {
		t.Fatalf("depth after middle Finish = %d, want 2", coord.PendingDepth())
	}
	coord.Submit(SubmitApproval{Identity: "head", Tool: "head", Decision: "allow_once"})
	if got := (<-head).Decision; got != "allow_once" {
		t.Errorf("head decision = %q, want allow_once", got)
	}
	select {
	case <-tail:
		t.Fatal("tail received submission before head was finished")
	default:
	}
	coord.Finish(head)
	coord.Submit(SubmitApproval{Identity: "tail", Tool: "tail", Decision: "deny"})
	if got := (<-tail).Decision; got != "deny" {
		t.Errorf("tail decision = %q, want deny", got)
	}
	coord.Finish(tail)
	if coord.HasPending() || coord.PendingDepth() != 0 {
		t.Fatalf("final pending state = %v/%d, want false/0", coord.HasPending(), coord.PendingDepth())
	}
}

func TestApprovalCoordinatorDuplicateSubmitClaimsOnlyHead(t *testing.T) {
	coord := &ApprovalCoordinator{}
	head := coord.Begin("head-id", "head", "", "", "")
	tail := coord.Begin("tail-id", "tail", "", "", "")

	coord.Submit(SubmitApproval{Identity: "head-id", Tool: "head", Decision: "first"})
	coord.Submit(SubmitApproval{Identity: "head-id", Tool: "head", Decision: "duplicate"})

	if got := (<-head).Decision; got != "first" {
		t.Fatalf("head decision = %q, want first", got)
	}
	select {
	case got := <-tail:
		t.Fatalf("tail received stale decision %q", got.Decision)
	default:
	}
	if got := coord.HeadIdentity(); got != "tail-id" {
		t.Fatalf("head identity = %q, want tail-id", got)
	}
}

func TestApprovalCoordinatorDuplicateSameToolIdentityDoesNotAdvanceTail(t *testing.T) {
	coord := &ApprovalCoordinator{}
	first := coord.Begin("call-1", "bash", "", "", "")
	second := coord.Begin("call-2", "bash", "", "", "")

	coord.Submit(SubmitApproval{Identity: "call-1", Tool: "bash", Decision: "allow_once"})
	if got := <-first; got.Decision != "allow_once" {
		t.Fatalf("first decision = %q, want allow_once", got.Decision)
	}
	for i := 0; i < 2; i++ {
		coord.Submit(SubmitApproval{Identity: "call-1", Tool: "bash", Decision: "duplicate"})
	}
	select {
	case got := <-second:
		t.Fatalf("second received duplicate decision %q", got.Decision)
	default:
	}
	if got := coord.HeadIdentity(); got != "call-2" {
		t.Fatalf("head identity = %q, want call-2", got)
	}
}

func TestApprovalCoordinatorConcurrentBeginAndSubmit(t *testing.T) {
	coord := &ApprovalCoordinator{}
	started := make(chan chan SubmitApproval, 2)
	for _, name := range []string{"a", "b"} {
		go func() { started <- coord.Begin(name, name, "", "", "") }()
	}
	channels := []chan SubmitApproval{<-started, <-started}
	coord.Submit(SubmitApproval{Identity: "a", Decision: "first"})
	var first chan SubmitApproval
	select {
	case <-channels[0]:
		first = channels[0]
	case <-channels[1]:
		first = channels[1]
	case <-time.After(time.Second):
		t.Fatal("first response timed out")
	}
	coord.Finish(first)
	coord.Submit(SubmitApproval{Identity: "b", Decision: "second"})
	var second chan SubmitApproval
	if first == channels[0] {
		second = channels[1]
	} else {
		second = channels[0]
	}
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("second response timed out")
	}
	coord.Finish(second)
}

func TestApprovalCoordinatorSubmitFinishRace(t *testing.T) {
	coord := &ApprovalCoordinator{}
	a := coord.Begin("a", "a", "", "", "")
	b := coord.Begin("b", "b", "", "", "")
	finishStarted := make(chan struct{})
	finishDone := make(chan struct{})
	go func() {
		close(finishStarted)
		coord.Finish(a)
		close(finishDone)
	}()
	<-finishStarted
	coord.Submit(SubmitApproval{Identity: "b", Tool: "b", Decision: "allow_once"})
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
