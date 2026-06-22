package agent

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestSteerInjection(t *testing.T) {
	t.Run("delivers steer message into conversation between turns", func(t *testing.T) {
		providerStub := &fakeProvider{
			responses: []provider.ChatResponse{
				{
					Message: provider.Message{
						Role: provider.MessageRoleAssistant,
						ToolCalls: []provider.ToolCall{
							{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "a.txt"}},
						},
					},
					FinishReason: "tool_calls",
					Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
				},
				{
					Message: provider.Message{
						Role:    provider.MessageRoleAssistant,
						Content: "done",
					},
					FinishReason: "stop",
					Usage:        &provider.UsageStats{TotalTokens: 3, CompletionTokens: 3},
				},
			},
		}
		executor := &fakeExecutor{
			execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
				return map[string]any{"contents": "hello"}, nil
			},
		}

		var steerCallCount int
		var events []output.Event
		state, err := NewRunner().Run(context.Background(), RunRequest{
			Provider: providerStub,
			Executor: executor,
			Prompt: prompt.AssemblyOptions{
				Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "fix the bug"}},
			},
			Limits: Limits{MaxTurns: 4, MaxTokens: 100},
			DrainSteers: func() []SteerMessage {
				steerCallCount++
				if steerCallCount == 1 {
					return []SteerMessage{{Text: "please focus on correctness"}}
				}
				return nil
			},
			Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if got, want := state.StopReason, StopReasonComplete; got != want {
			t.Fatalf("StopReason = %q, want %q", got, want)
		}

		// The steer message must appear in the conversation as a user message.
		var found bool
		for _, msg := range state.Conversation {
			if msg.Role == MessageRoleUser && msg.Content == "please focus on correctness" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("steer message not found in conversation: %v", state.Conversation)
		}

		// The steer message must also appear in the lineage.
		var foundInLineage bool
		for _, msg := range state.Lineage.FullMessages() {
			if msg.Role == MessageRoleUser && msg.Content == "please focus on correctness" {
				foundInLineage = true
				break
			}
		}
		if !foundInLineage {
			t.Fatal("steer message not found in lineage")
		}

		// A SteerReceivedEvent must have been emitted.
		var steerEvent *output.Event
		for i, ev := range events {
			if ev.Type == output.EventTypeSteerReceived {
				steerEvent = &events[i]
				break
			}
		}
		if steerEvent == nil {
			t.Fatalf("no %s event emitted; events: %v", output.EventTypeSteerReceived, eventTypes(events))
		}
		payload, ok := steerEvent.Payload.(output.SteerReceivedEvent)
		if !ok {
			t.Fatalf("steer event payload type = %T, want output.SteerReceivedEvent", steerEvent.Payload)
		}
		if got, want := payload.Text, "please focus on correctness"; got != want {
			t.Fatalf("steer event text = %q, want %q", got, want)
		}
	})

	t.Run("nil DrainSteers does not panic and does not alter behavior", func(t *testing.T) {
		providerStub := &fakeProvider{
			responses: []provider.ChatResponse{
				{
					Message: provider.Message{
						Role:    provider.MessageRoleAssistant,
						Content: "done",
					},
					FinishReason: "stop",
					Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
				},
			},
		}

		state, err := NewRunner().Run(context.Background(), RunRequest{
			Provider: providerStub,
			Executor: &fakeExecutor{},
			Prompt: prompt.AssemblyOptions{
				Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
			},
			Limits:      Limits{MaxTurns: 2, MaxTokens: 100},
			DrainSteers: nil,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if got, want := state.StopReason, StopReasonComplete; got != want {
			t.Fatalf("StopReason = %q, want %q", got, want)
		}
	})

	t.Run("empty DrainSteers does not block the loop", func(t *testing.T) {
		providerStub := &fakeProvider{
			responses: []provider.ChatResponse{
				{
					Message: provider.Message{
						Role:    provider.MessageRoleAssistant,
						Content: "done",
					},
					FinishReason: "stop",
					Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
				},
			},
		}

		var events []output.Event
		state, err := NewRunner().Run(context.Background(), RunRequest{
			Provider: providerStub,
			Executor: &fakeExecutor{},
			Prompt: prompt.AssemblyOptions{
				Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
			},
			Limits:      Limits{MaxTurns: 2, MaxTokens: 100},
			DrainSteers: func() []SteerMessage { return nil },
			Events:      output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if got, want := state.StopReason, StopReasonComplete; got != want {
			t.Fatalf("StopReason = %q, want %q", got, want)
		}
		// No steer event should be emitted when the drain returns nothing.
		for _, ev := range events {
			if ev.Type == output.EventTypeSteerReceived {
				t.Fatalf("unexpected %s event when drain returns nothing", output.EventTypeSteerReceived)
			}
		}
	})
}

func TestMergeSteers(t *testing.T) {
	tests := []struct {
		name       string
		steers     []SteerMessage
		wantText   string
		wantImages int
	}{
		{
			name:       "single steer without images",
			steers:     []SteerMessage{{Text: "fix the bug"}},
			wantText:   "fix the bug",
			wantImages: 0,
		},
		{
			name:       "single steer with images",
			steers:     []SteerMessage{{Text: "see [Image 1]", Images: []ImageBlock{{MediaType: "image/png", Data: "abc"}}}},
			wantText:   "see [Image 1]",
			wantImages: 1,
		},
		{
			name: "two steers without images",
			steers: []SteerMessage{
				{Text: "first instruction"},
				{Text: "second instruction"},
			},
			wantText:   "first instruction\n\nsecond instruction",
			wantImages: 0,
		},
		{
			name: "two steers with images renumbered",
			steers: []SteerMessage{
				{Text: "see [Image 1] and [Image 2]", Images: []ImageBlock{{MediaType: "image/png"}, {MediaType: "image/png"}}},
				{Text: "also [Image 1]", Images: []ImageBlock{{MediaType: "image/jpeg"}}},
			},
			wantText:   "see [Image 1] and [Image 2]\n\nalso [Image 3]",
			wantImages: 3,
		},
		{
			name: "three steers with cumulative renumbering",
			steers: []SteerMessage{
				{Text: "[Image 1]", Images: []ImageBlock{{MediaType: "image/png"}}},
				{Text: "[Image 1]", Images: []ImageBlock{{MediaType: "image/png"}}},
				{Text: "[Image 1] and [Image 2]", Images: []ImageBlock{{MediaType: "image/png"}, {MediaType: "image/png"}}},
			},
			wantText:   "[Image 1]\n\n[Image 2]\n\n[Image 3] and [Image 4]",
			wantImages: 4,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeSteers(tc.steers)
			if got.Content != tc.wantText {
				t.Errorf("Content = %q, want %q", got.Content, tc.wantText)
			}
			if len(got.Images) != tc.wantImages {
				t.Errorf("len(Images) = %d, want %d", len(got.Images), tc.wantImages)
			}
			if got.Role != MessageRoleUser {
				t.Errorf("Role = %q, want %q", got.Role, MessageRoleUser)
			}
		})
	}
}

func TestRenumberMarkers(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		offset int
		want   string
	}{
		{
			name:   "no markers",
			text:   "plain text",
			offset: 2,
			want:   "plain text",
		},
		{
			name:   "single marker",
			text:   "see [Image 1]",
			offset: 3,
			want:   "see [Image 4]",
		},
		{
			name:   "multiple markers",
			text:   "[Image 1] and [Image 2]",
			offset: 5,
			want:   "[Image 6] and [Image 7]",
		},
		{
			name:   "zero offset is identity",
			text:   "[Image 3]",
			offset: 0,
			want:   "[Image 3]",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := renumberMarkers(tc.text, tc.offset)
			if got != tc.want {
				t.Errorf("renumberMarkers(%q, %d) = %q, want %q", tc.text, tc.offset, got, tc.want)
			}
		})
	}
}

func TestInitialConversationTurnCount(t *testing.T) {
	tests := []struct {
		name     string
		messages []Message
		want     int
	}{
		{
			name:     "empty conversation",
			messages: nil,
			want:     0,
		},
		{
			name: "uses highest positive turn",
			messages: []Message{
				{Role: MessageRoleUser, Turn: 1},
				{Role: MessageRoleAssistant, Turn: 4},
				{Role: MessageRoleTool, Turn: 3},
				{Role: MessageRoleTool, Turn: 0},
				{Role: MessageRoleAssistant, Turn: -2},
			},
			want: 4,
		},
		{
			name: "ignores non-positive turns",
			messages: []Message{
				{Role: MessageRoleUser, Turn: 0},
				{Role: MessageRoleAssistant, Turn: -1},
			},
			want: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := initialConversationTurnCount(tc.messages); got != tc.want {
				t.Fatalf("initialConversationTurnCount() = %d, want %d", got, tc.want)
			}
		})
	}
}
