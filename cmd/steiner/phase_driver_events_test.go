package main

import (
	"context"
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
)

func TestRunPhaseOnDriverForwardsConversationStateOnlyToInteractiveLaunch(t *testing.T) {
	tests := []struct {
		name        string
		interactive bool
	}{
		{name: "interactive", interactive: true},
		{name: "headless", interactive: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := make(chan output.Event, 8)
			h := &phaseHarness{bg: &phaseBackground{}}
			released := false
			stale := false
			host := h.host(func(_ context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
				return agent.DriverRunOutput{Conversation: assistantReply("finished", in.Conversation)}, nil
			})
			host.events = output.SinkFunc(func(event output.Event) {
				if event.Type == output.EventTypeConversationState {
					if released {
						stale = true
					}
					events <- event
				}
			})
			in := h.input()
			if tt.interactive {
				in.RegisterControl = func(oneshot.PhaseControl) func() {
					return func() { released = true }
				}
			}

			if _, err := runPhaseOnDriver(context.Background(), in, host); err != nil {
				t.Fatalf("runPhaseOnDriver() error = %v", err)
			}
			if tt.interactive {
				var states []string
				for {
					select {
					case event := <-events:
						payload, ok := event.Payload.(output.ConversationStateEvent)
						if ok {
							states = append(states, payload.State)
						}
					default:
						if !slices.Equal(states, []string{"idle", "generating", "idle"}) {
							t.Fatalf("interactive phase states = %v, want idle, generating, terminal idle", states)
						}
						if stale {
							t.Fatal("conversation state event arrived after phase control release")
						}
						return
					}
				}
			} else if len(events) != 0 {
				t.Fatalf("headless phase emitted conversation state events: %d", len(events))
			}
		})
	}
}
