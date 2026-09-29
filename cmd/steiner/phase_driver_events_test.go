package main

import (
	"context"
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
			host := h.host(func(_ context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
				return agent.DriverRunOutput{Conversation: assistantReply("finished", in.Conversation)}, nil
			})
			host.events = output.SinkFunc(func(event output.Event) {
				if event.Type == output.EventTypeConversationState {
					events <- event
				}
			})
			in := h.input()
			if tt.interactive {
				in.RegisterControl = func(oneshot.PhaseControl) func() { return func() {} }
			}

			if _, err := runPhaseOnDriver(context.Background(), in, host); err != nil {
				t.Fatalf("runPhaseOnDriver() error = %v", err)
			}
			if tt.interactive {
				foundGenerating := false
				for {
					select {
					case event := <-events:
						payload, ok := event.Payload.(output.ConversationStateEvent)
						if ok && payload.State == "generating" {
							foundGenerating = true
						}
					default:
						if !foundGenerating {
							t.Fatal("interactive phase conversation state did not reach session events")
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
