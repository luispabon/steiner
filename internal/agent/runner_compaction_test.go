package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

//nolint:gocyclo
func TestRunnerKeepsPromptBoundedAndRetainsDurableContext(t *testing.T) {
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
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_2", Name: "read", Arguments: map[string]any{"path": "b.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_3", Name: "read", Arguments: map[string]any{"path": "c.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return tool.ExecutionResult{
				Value: map[string]any{"contents": "alpha"},
			}, nil
		},
	}

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}},
			ContextState: prompt.DurableContextState{
				RetainedSummaries: []prompt.DurableSummaryEntry{
					{Title: "prior work", Text: "keep prompt assembly policy-driven", Source: "assistant", Turn: 1},
				},
			},
			Policy: prompt.AssemblyPolicy{
				Compaction:  prompt.CompactionPolicy{SummaryBytes: 256},
				ToolSummary: prompt.ToolSummaryPolicy{MaxBytes: 32},
			},
		},
		Limits: Limits{MaxTurns: 8, MaxTokens: 2000},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(providerStub.requests), 4; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}

	if got, want := len(state.Lineage.Generations), 1; got != want {
		t.Fatalf("lineage generations = %d, want %d", got, want)
	}
	if got, want := len(state.Lineage.FullMessages()), len(state.Conversation); got != want {
		t.Fatalf("lineage/full conversation len = %d, want %d", got, want)
	}
	if got, want := state.Lineage.FullMessages()[0].Content, "start"; got != want {
		t.Fatalf("lineage first message = %q, want %q", got, want)
	}

	// RetainedSummaries survive across turns via the compaction path.
	if got, want := len(state.Context.RetainedSummaries), 1; got != want {
		t.Fatalf("retained summaries = %d, want %d", got, want)
	}
	if got, want := state.Context.RetainedSummaries[0].Text, "keep prompt assembly policy-driven"; got != want {
		t.Fatalf("retained summary text = %q, want %q", got, want)
	}
}

func TestRunnerEmitsContextDiagnosticsForBudgetPressureAndCompaction(t *testing.T) {
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
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_2", Name: "read", Arguments: map[string]any{"path": "b.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return tool.ExecutionResult{
				Value: map[string]any{"contents": "alpha"},
			}, nil
		},
	}

	var events []output.Event
	_, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}},
			ContextState: prompt.DurableContextState{
				RetainedSummaries: []prompt.DurableSummaryEntry{
					{Title: "prior", Text: strings.Repeat("summary ", 8), Source: "user", Turn: 1},
				},
			},
			Policy: prompt.AssemblyPolicy{
				Compaction: prompt.CompactionPolicy{
					SummaryBytes: 64,
				},
			},
		},
		Limits: Limits{MaxTurns: 6, MaxTokens: 100},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var kinds []string
	for _, event := range events {
		if event.Type != output.EventTypeContextDiagnostics {
			continue
		}
		kinds = append(kinds, output.ContextDiagnosticKind(event.Payload))
	}
	if !containsString(kinds, "budget") {
		t.Fatalf("diagnostic kinds = %v, want budget event", kinds)
	}
	if containsString(kinds, "compaction") {
		t.Fatalf("diagnostic kinds = %v, want no compaction event at this stage", kinds)
	}
}

//nolint:gocyclo
func TestRunnerRecompactsUntilTheBudgetFits(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "first handoff",
				},
				FinishReason: "stop",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "second handoff",
				},
				FinishReason: "stop",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
			},
		},
	}
	executor := &fakeExecutor{}

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: executor,
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:               2500,
			MaxCompletionTokens:       32,
			SummaryMaxTokens:          32,
			NormalSummaryMaxTokens:    32,
			EmergencySummaryMaxTokens: 16,
		},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{
				{Role: provider.MessageRoleUser, Content: strings.Repeat("initial request ", 120)},
				{Role: provider.MessageRoleAssistant, Content: strings.Repeat("initial answer ", 96)},
				{Role: provider.MessageRoleUser, Content: strings.Repeat("second request ", 112)},
				{Role: provider.MessageRoleAssistant, Content: strings.Repeat("second answer ", 88)},
				{Role: provider.MessageRoleUser, Content: strings.Repeat("third request ", 96)},
				{Role: provider.MessageRoleAssistant, Content: strings.Repeat("third answer ", 80)},
				{Role: provider.MessageRoleUser, Content: strings.Repeat("follow up request ", 88)},
				{Role: provider.MessageRoleAssistant, Content: strings.Repeat("follow up answer ", 72)},
			},
			ToolResults: []provider.Message{
				{Role: provider.MessageRoleTool, Content: strings.Repeat("tool output ", 90)},
			},
			Policy: prompt.AssemblyPolicy{
				Compaction: prompt.CompactionPolicy{SummaryBytes: 128},
			},
		},
		Limits: Limits{MaxTurns: 3},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(providerStub.requests), 2; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
	if got, want := len(state.Lineage.Generations), 2; got != want {
		t.Fatalf("lineage generations = %d, want %d", got, want)
	}
	if got := len(state.Lineage.Generations[1].SummaryPrefix); got == 0 {
		t.Fatal("latest summary prefix = empty, want retained compaction summary")
	}

	var compactionCount int
	var sessionHealthCount int
	var budgetCount int
	var compactionCounts []int
	for _, event := range events {
		if event.Type != output.EventTypeContextDiagnostics {
			continue
		}
		switch output.ContextDiagnosticKind(event.Payload) {
		case "compaction":
			payload, ok := output.AsContextCompactionEvent(event.Payload)
			if !ok {
				t.Fatalf("diagnostic payload type = %T, want output.ContextCompactionEvent", event.Payload)
			}
			if payload.Severity == "compacting" {
				continue
			}
			compactionCount++
			compactionCounts = append(compactionCounts, payload.CompactionCount)
			if payload.Severity == "" {
				t.Fatalf("compaction payload = %#v, want severity", payload)
			}
			if payload.SessionState == "" {
				t.Fatalf("compaction payload = %#v, want session state", payload)
			}
			if payload.RestartGuidance == "" {
				t.Fatalf("compaction payload = %#v, want restart guidance", payload)
			}
		case "session_health":
			payload, ok := output.AsContextSessionHealthEvent(event.Payload)
			if !ok {
				t.Fatalf("diagnostic payload type = %T, want output.ContextSessionHealthEvent", event.Payload)
			}
			if payload.Severity == "compacting" {
				continue
			}
			sessionHealthCount++
			if payload.CompactionCount == 0 {
				t.Fatalf("session health payload = %#v, want compaction count", payload)
			}
			if payload.Severity == "" {
				t.Fatalf("session health payload = %#v, want severity", payload)
			}
		case "budget":
			payload, ok := output.AsContextBudgetEvent(event.Payload)
			if !ok {
				t.Fatalf("diagnostic payload type = %T, want output.ContextBudgetEvent", event.Payload)
			}
			if payload.PromptTokens > 0 || payload.TotalTokens > 0 {
				budgetCount++
			}
		}
	}
	if got, want := compactionCount, 1; got != want {
		t.Fatalf("compaction events = %d, want %d", got, want)
	}
	if got, want := sessionHealthCount, 1; got != want {
		t.Fatalf("session health events = %d, want %d", got, want)
	}
	if got, want := len(compactionCounts), 1; got != want {
		t.Fatalf("compaction counts = %v, want %d entries", compactionCounts, want)
	}
	if got, want := compactionCounts[0], 1; got != want {
		t.Fatalf("first compaction count = %d, want %d", got, want)
	}
	if got, want := budgetCount, 2; got != want {
		t.Fatalf("token budget diagnostics = %d, want %d", got, want)
	}
}
