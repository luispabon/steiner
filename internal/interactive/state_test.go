package interactive

import (
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

func TestActiveRunControllerSteer(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "send and receive via DrainSteers",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				c.Steer("hello", nil)
				got := c.DrainSteers()
				if len(got) != 1 || got[0].Text != "hello" {
					t.Errorf("DrainSteers() = %+v, want [{hello nil}]", got)
				}
			},
		},
		{
			name: "DrainSteers returns nil when no message pending",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				got := c.DrainSteers()
				if len(got) != 0 {
					t.Errorf("DrainSteers() = %+v, want empty", got)
				}
			},
		},
		{
			name: "FIFO ordering: DrainSteers returns all steers in order",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				c.Steer("first", nil)
				c.Steer("second", nil)
				c.Steer("third", nil)
				got := c.DrainSteers()
				if len(got) != 3 {
					t.Fatalf("DrainSteers() = %+v, want 3 items", got)
				}
				if got[0].Text != "first" || got[1].Text != "second" || got[2].Text != "third" {
					t.Errorf("DrainSteers() = %+v, want [first second third]", got)
				}
			},
		},
		{
			name: "Steer with images preserves them in DrainSteers",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				imgs := []agent.ImageBlock{{MediaType: "image/png", Data: "abc"}}
				c.Steer("see image", imgs)
				got := c.DrainSteers()
				if len(got) != 1 {
					t.Fatalf("DrainSteers() = %+v, want 1 item", got)
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
			name: "DrainSteers empties queue on second call",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				c.Steer("msg", nil)
				c.DrainSteers()
				got := c.DrainSteers()
				if len(got) != 0 {
					t.Errorf("second DrainSteers() = %+v, want empty", got)
				}
			},
		},
		{
			name: "Clear empties drain queue",
			test: func(t *testing.T) {
				c := NewActiveRunController()
				c.Steer("pending", nil)
				c.Clear()
				got := c.DrainSteers()
				if len(got) != 0 {
					t.Errorf("after Clear(), DrainSteers() = %+v, want empty", got)
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
	head := coord.Begin("head", "", "", "code")
	middle := coord.Begin("middle", "", "", "review")
	tail := coord.Begin("tail", "", "", "plan")
	if !coord.HasPending() || coord.PendingDepth() != 3 {
		t.Fatalf("pending state = %v/%d, want true/3", coord.HasPending(), coord.PendingDepth())
	}

	coord.Finish(middle)
	if coord.PendingDepth() != 2 {
		t.Fatalf("depth after middle Finish = %d, want 2", coord.PendingDepth())
	}
	coord.Submit(SubmitApproval{Tool: "head", Decision: "allow_once"})
	if got := (<-head).Decision; got != "allow_once" {
		t.Errorf("head decision = %q, want allow_once", got)
	}
	select {
	case <-tail:
		t.Fatal("tail received submission before head was finished")
	default:
	}
	coord.Finish(head)
	coord.Submit(SubmitApproval{Tool: "tail", Decision: "deny"})
	if got := (<-tail).Decision; got != "deny" {
		t.Errorf("tail decision = %q, want deny", got)
	}
	coord.Finish(tail)
	if coord.HasPending() || coord.PendingDepth() != 0 {
		t.Fatalf("final pending state = %v/%d, want false/0", coord.HasPending(), coord.PendingDepth())
	}
}

func TestApprovalCoordinatorConcurrentBeginAndSubmit(t *testing.T) {
	coord := &ApprovalCoordinator{}
	started := make(chan chan SubmitApproval, 2)
	for _, name := range []string{"a", "b"} {
		go func() { started <- coord.Begin(name, "", "", "") }()
	}
	channels := []chan SubmitApproval{<-started, <-started}
	coord.Submit(SubmitApproval{Decision: "first"})
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
	coord.Submit(SubmitApproval{Decision: "second"})
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
	a := coord.Begin("a", "", "", "")
	b := coord.Begin("b", "", "", "")
	finishStarted := make(chan struct{})
	finishDone := make(chan struct{})
	go func() {
		close(finishStarted)
		coord.Finish(a)
		close(finishDone)
	}()
	<-finishStarted
	coord.Submit(SubmitApproval{Tool: "b", Decision: "allow_once"})
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
