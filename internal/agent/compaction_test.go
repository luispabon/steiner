package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestCompactionEscalationPolicy(t *testing.T) {
	stableFit := prompt.RequestTokenBudget{ContextSize: 400, TotalTokens: 460}
	fragileFit := prompt.RequestTokenBudget{ContextSize: 400, TotalTokens: 560}

	tests := []struct {
		name         string
		count        int
		fit          prompt.RequestTokenBudget
		wantSeverity string
		wantState    string
	}{
		{
			name:         "first compaction stays informational when healthy",
			count:        1,
			fit:          stableFit,
			wantSeverity: "info",
			wantState:    "stable",
		},
		{
			name:         "second compaction warns when healthy",
			count:        2,
			fit:          stableFit,
			wantSeverity: "warning",
			wantState:    "fragile",
		},
		{
			name:         "third compaction is critical when healthy",
			count:        3,
			fit:          stableFit,
			wantSeverity: "critical",
			wantState:    "likely_lossy",
		},
		{
			name:         "first compaction escalates early when fragile",
			count:        1,
			fit:          fragileFit,
			wantSeverity: "warning",
			wantState:    "fragile",
		},
		{
			name:         "second compaction becomes critical when fragile",
			count:        2,
			fit:          fragileFit,
			wantSeverity: "critical",
			wantState:    "likely_lossy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			escalation := compactionEscalationForFit(tt.count, tt.fit)
			if got, want := escalation.Severity, tt.wantSeverity; got != want {
				t.Fatalf("severity = %q, want %q", got, want)
			}
			if got, want := escalation.SessionState, tt.wantState; got != want {
				t.Fatalf("session state = %q, want %q", got, want)
			}
			if strings.TrimSpace(escalation.RestartGuidance) == "" {
				t.Fatal("restart guidance = empty, want non-empty")
			}
		})
	}
}

func TestCompactionCandidateFallbackKeepsRicherGenerationWhenAvailable(t *testing.T) {
	t.Parallel()

	lineage := ConversationLineage{
		Generations: []ConversationGeneration{
			newConversationGeneration(1, nil, []Message{
				{Role: MessageRoleUser, Content: "gen1 user one"},
				{Role: MessageRoleAssistant, Content: "gen1 assistant one"},
				{Role: MessageRoleUser, Content: "gen1 user two"},
			}),
			newConversationGeneration(2, []Message{
				{Role: MessageRoleSummary, Content: "gen2 summary prefix"},
			}, []Message{
				{Role: MessageRoleUser, Content: "gen2 retained user"},
				{Role: MessageRoleAssistant, Content: "gen2 retained assistant"},
			}),
			newConversationGeneration(3, []Message{
				{Role: MessageRoleSummary, Content: "gen3 summary prefix"},
			}, []Message{
				{Role: MessageRoleUser, Content: "gen3 retained user"},
			}),
		},
	}

	candidate, ok := selectCompactionCandidate(lineage, map[string]bool{
		compactionCandidateKey(ConversationCandidate{GenerationID: 1, View: ConversationViewFull}): true,
	})
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	if got, want := candidate.GenerationID, 2; got != want {
		t.Fatalf("candidate generation = %d, want %d", got, want)
	}
	if got, want := candidate.View, ConversationViewFull; got != want {
		t.Fatalf("candidate view = %q, want %q", got, want)
	}

	retained := retainedMessagesForCandidate(lineage, candidate)
	if got, want := len(retained), 2; got != want {
		t.Fatalf("retained messages = %d, want %d", got, want)
	}
	if got, want := retained[0].Content, "gen2 retained user"; got != want {
		t.Fatalf("retained[0] = %q, want %q", got, want)
	}
	if got, want := retained[1].Content, "gen2 retained assistant"; got != want {
		t.Fatalf("retained[1] = %q, want %q", got, want)
	}
}

func TestSummarizeCompactorPreservesCurrentBehavior(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "summary handoff text",
				},
				FinishReason: "stop",
			},
		},
	}

	state := RunState{
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "turn 1 user"},
			{Role: MessageRoleAssistant, Content: "turn 1 assistant"},
			{Role: MessageRoleUser, Content: "turn 2 user"},
			{Role: MessageRoleAssistant, Content: "turn 2 assistant"},
		},
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleUser, Content: "turn 1 user"},
			{Role: MessageRoleAssistant, Content: "turn 1 assistant"},
			{Role: MessageRoleUser, Content: "turn 2 user"},
			{Role: MessageRoleAssistant, Content: "turn 2 assistant"},
		}),
	}

	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}

	req := RunRequest{
		ContextManager: NewContextManager("smart", config.ContextManagementConfig{
			CompactionStrategy: config.CompactionStrategySummarize,
		}),
		Provider:      providerStub,
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         100000,
			MaxCompletionTokens: 256,
			SafetyMarginTokens:  0,
			SummaryMaxTokens:    128,
		},
	}
	outcome, err := compactorForRequest(req).Compact(
		context.Background(),
		req,
		state,
		3,
		candidate,
	)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if !outcome.Applied {
		t.Fatal("Applied = false, want true")
	}
	if got, want := len(providerStub.requests), 1; got != want {
		t.Fatalf("ChatCompletion calls = %d, want %d", got, want)
	}
	if got, want := len(outcome.State.Conversation), 1; got != want {
		t.Fatalf("len(conversation) = %d, want 1 (summary only, no retained messages)", got)
	}
	if got, want := outcome.State.Conversation[0].Role, MessageRoleSummary; got != want {
		t.Fatalf("conversation[0].role = %q, want %q", got, want)
	}
	if got, want := outcome.State.Conversation[0].Content, "summary handoff text"; got != want {
		t.Fatalf("conversation[0].content = %q, want %q", got, want)
	}
	if got, want := len(outcome.State.Context.RetainedSummaries), 1; got != want {
		t.Fatalf("retained summaries = %d, want %d", got, want)
	}
	if got, want := outcome.State.Context.RetainedSummaries[0].Text, "summary handoff text"; got != want {
		t.Fatalf("retained summary text = %q, want %q", got, want)
	}
}

func TestSummarizeCompactorDoesNotRetainMessagesOnRecompaction(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "recompaction summary",
				},
				FinishReason: "stop",
			},
		},
	}

	lineage := ConversationLineage{
		Generations: []ConversationGeneration{
			newConversationGeneration(1, nil, []Message{
				{Role: MessageRoleUser, Content: "original user"},
				{Role: MessageRoleAssistant, Content: "original assistant"},
			}),
			newConversationGeneration(2, []Message{
				{Role: MessageRoleSummary, Content: "first compaction summary"},
			}, []Message{
				{Role: MessageRoleUser, Content: "post-compaction user"},
				{Role: MessageRoleAssistant, Content: "post-compaction assistant"},
			}),
		},
		NextGenerationID: 3,
	}

	state := RunState{
		Conversation: lineage.FullMessages(),
		Lineage:      lineage,
	}

	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}

	outcome, err := summarizeCompactor{}.Compact(
		context.Background(),
		RunRequest{
			Provider:      providerStub,
			ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
			ModelBudget: prompt.ModelTokenBudget{
				ContextSize:         100000,
				MaxCompletionTokens: 256,
				SafetyMarginTokens:  0,
				SummaryMaxTokens:    128,
			},
		},
		state,
		5,
		candidate,
	)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if !outcome.Applied {
		t.Fatal("Applied = false, want true")
	}
	if got, want := len(providerStub.requests), 1; got != want {
		t.Fatalf("ChatCompletion calls = %d, want %d", got, want)
	}
	if got, want := len(outcome.State.Conversation), 1; got != want {
		t.Fatalf("len(conversation) = %d, want 1 (summary only, no old messages retained)", got)
	}
	if got, want := outcome.State.Conversation[0].Role, MessageRoleSummary; got != want {
		t.Fatalf("conversation[0].role = %q, want %q", got, want)
	}
	if got, want := outcome.State.Conversation[0].Content, "recompaction summary"; got != want {
		t.Fatalf("conversation[0].content = %q, want %q", got, want)
	}
}

func TestDropCompactorKeepsRecentTurnsAndMarker(t *testing.T) {
	state := RunState{
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "turn 1 user"},
			{Role: MessageRoleAssistant, Content: "turn 1 assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "read", Arguments: map[string]any{"path": "one.txt"}}}},
			{Role: MessageRoleTool, Name: "read", ToolCallID: "call-1", Content: "turn 1 result"},
			{Role: MessageRoleUser, Content: "turn 2 user"},
			{Role: MessageRoleAssistant, Content: "turn 2 assistant", ToolCalls: []ToolCall{{ID: "call-2", Name: "bash", Arguments: map[string]any{"command": "echo two"}}}},
			{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-2", Content: "turn 2 result"},
			{Role: MessageRoleUser, Content: "turn 3 user"},
			{Role: MessageRoleAssistant, Content: "turn 3 assistant"},
			{Role: MessageRoleUser, Content: "turn 4 user"},
			{Role: MessageRoleAssistant, Content: "turn 4 assistant"},
		},
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleUser, Content: "turn 1 user"},
			{Role: MessageRoleAssistant, Content: "turn 1 assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "read", Arguments: map[string]any{"path": "one.txt"}}}},
			{Role: MessageRoleTool, Name: "read", ToolCallID: "call-1", Content: "turn 1 result"},
			{Role: MessageRoleUser, Content: "turn 2 user"},
			{Role: MessageRoleAssistant, Content: "turn 2 assistant", ToolCalls: []ToolCall{{ID: "call-2", Name: "bash", Arguments: map[string]any{"command": "echo two"}}}},
			{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-2", Content: "turn 2 result"},
			{Role: MessageRoleUser, Content: "turn 3 user"},
			{Role: MessageRoleAssistant, Content: "turn 3 assistant"},
			{Role: MessageRoleUser, Content: "turn 4 user"},
			{Role: MessageRoleAssistant, Content: "turn 4 assistant"},
		}),
	}

	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}

	dropReq := RunRequest{
		ContextManager: NewContextManager("smart", config.ContextManagementConfig{
			CompactionStrategy: config.CompactionStrategyDrop,
		}),
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 256,
			SafetyMarginTokens:  0,
			SummaryMaxTokens:    128,
		},
	}
	outcome, err := compactorForRequest(dropReq).Compact(
		context.Background(),
		dropReq,
		state,
		9,
		candidate,
	)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if !outcome.Applied {
		t.Fatal("Applied = false, want true")
	}
	if got, want := outcome.State.Conversation[0].Content, dropCompactionMarker; got != want {
		t.Fatalf("marker = %q, want %q", got, want)
	}
	if got, want := outcome.State.Conversation[1].Content, "turn 2 user"; got != want {
		t.Fatalf("conversation[1] = %q, want %q", got, want)
	}
	if messageContentsContain(ToProviderMessages(outcome.State.Conversation), "turn 1 user") {
		t.Fatal("turn 1 user was retained, want it dropped")
	}
	if !messageContentsContain(ToProviderMessages(outcome.State.Conversation), "turn 2 result") {
		t.Fatal("turn 2 tool result missing, want turn pair preserved")
	}
	if !messageContentsContain(ToProviderMessages(outcome.State.Conversation), "turn 3 assistant") {
		t.Fatal("turn 3 assistant missing, want recent turns preserved")
	}
}

func TestCompactionResetsEpochStateAndEmitsResetDiagnostic(t *testing.T) {
	var events []output.Event
	cm := &SmartContextManager{
		compactionStrategy: config.CompactionStrategyDrop,
		epoch: EpochManager{
			maskingWindowTurns: 5,
			epochMaskBoundary:  7,
			epochStartTurn:     12,
		},
	}
	state := RunState{
		TurnCount: 12,
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "turn 1 user"},
			{Role: MessageRoleAssistant, Content: "turn 1 assistant"},
			{Role: MessageRoleUser, Content: "turn 2 user"},
			{Role: MessageRoleAssistant, Content: "turn 2 assistant"},
			{Role: MessageRoleUser, Content: "turn 3 user"},
			{Role: MessageRoleAssistant, Content: "turn 3 assistant"},
			{Role: MessageRoleUser, Content: "turn 4 user"},
			{Role: MessageRoleAssistant, Content: "turn 4 assistant"},
		},
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleUser, Content: "turn 1 user"},
			{Role: MessageRoleAssistant, Content: "turn 1 assistant"},
			{Role: MessageRoleUser, Content: "turn 2 user"},
			{Role: MessageRoleAssistant, Content: "turn 2 assistant"},
			{Role: MessageRoleUser, Content: "turn 3 user"},
			{Role: MessageRoleAssistant, Content: "turn 3 assistant"},
			{Role: MessageRoleUser, Content: "turn 4 user"},
			{Role: MessageRoleAssistant, Content: "turn 4 assistant"},
		}),
	}

	var skipped = map[string]bool{}
	var compactionCount int
	sink := output.SinkFunc(func(event output.Event) { events = append(events, event) })
	cm.SetEventSink(sink)
	req := RunRequest{
		ContextManager: cm,
		ResolvedModel:  provider.ResolvedModel{BackendModelID: "test-model"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         100000,
			MaxCompletionTokens: 256,
			SafetyMarginTokens:  0,
			SummaryMaxTokens:    128,
		},
		Events: sink,
	}

	compacted, err := new(Runner).compactConversationForBudget(context.Background(), req, &state, 13, skipped, &compactionCount)
	if err != nil {
		t.Fatalf("compactConversationForBudget() error = %v", err)
	}
	if !compacted {
		t.Fatal("compactConversationForBudget() = false, want true")
	}
	if got, want := cm.epoch.epochMaskBoundary, 0; got != want {
		t.Fatalf("epochMaskBoundary = %d, want %d", got, want)
	}
	if got, want := cm.epoch.epochStartTurn, 13; got != want {
		t.Fatalf("epochStartTurn = %d, want %d", got, want)
	}

	foundReset := false
	for _, event := range events {
		if event.Type != output.EventTypeContextDiagnostics {
			continue
		}
		payload, ok := event.Payload.(output.ContextDiagnosticsEvent)
		if !ok || payload.Kind != "masking" || payload.Action != "reset" {
			continue
		}
		foundReset = true
		if payload.EpochStatus != "reset" {
			t.Fatalf("epoch reset status = %q, want reset", payload.EpochStatus)
		}
		if payload.EpochStartTurn != 13 {
			t.Fatalf("epoch reset start turn = %d, want 13", payload.EpochStartTurn)
		}
	}
	if !foundReset {
		t.Fatal("missing epoch reset diagnostic")
	}
}

func TestHybridCompactorMasksBeforeSummarizing(t *testing.T) {
	providerStub := &fakeProvider{}
	state := RunState{
		Conversation: []Message{
			{Role: MessageRoleUser, Content: "turn 1 user"},
			{Role: MessageRoleAssistant, Content: "turn 1 assistant\nmore detail"},
			{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-1", Content: "turn 1 tool result"},
			{Role: MessageRoleUser, Content: "turn 2 user"},
			{Role: MessageRoleAssistant, Content: "turn 2 assistant\nmore detail"},
			{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-2", Content: "turn 2 tool result"},
			{Role: MessageRoleUser, Content: "turn 3 user"},
			{Role: MessageRoleAssistant, Content: "turn 3 assistant\nmore detail"},
		},
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleUser, Content: "turn 1 user"},
			{Role: MessageRoleAssistant, Content: "turn 1 assistant\nmore detail"},
			{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-1", Content: "turn 1 tool result"},
			{Role: MessageRoleUser, Content: "turn 2 user"},
			{Role: MessageRoleAssistant, Content: "turn 2 assistant\nmore detail"},
			{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-2", Content: "turn 2 tool result"},
			{Role: MessageRoleUser, Content: "turn 3 user"},
			{Role: MessageRoleAssistant, Content: "turn 3 assistant\nmore detail"},
		}),
	}

	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}

	hybridReq := RunRequest{
		ContextManager: NewContextManager("smart", config.ContextManagementConfig{
			CompactionStrategy: config.CompactionStrategyHybrid,
			MaskingWindowTurns: 1,
		}),
		Provider:      providerStub,
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         100000,
			MaxCompletionTokens: 256,
			SafetyMarginTokens:  0,
			SummaryMaxTokens:    128,
		},
	}
	outcome, err := compactorForRequest(hybridReq).Compact(
		context.Background(),
		hybridReq,
		state,
		5,
		candidate,
	)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if !outcome.Applied {
		t.Fatal("Applied = false, want true")
	}
	if got := len(providerStub.requests); got != 0 {
		t.Fatalf("summarize provider calls = %d, want 0", got)
	}
	if got, want := outcome.State.Conversation[1].Content, "turn 1 assistant"; got != want {
		t.Fatalf("turn 1 assistant = %q, want %q", got, want)
	}
	// With the 2-turn grace period, turn 2 assistant should remain unmasked.
	if got, want := outcome.State.Conversation[4].Content, "turn 2 assistant\nmore detail"; got != want {
		t.Fatalf("turn 2 assistant = %q, want %q", got, want)
	}
	if got := outcome.State.Conversation[7].Content; !strings.Contains(got, "more detail") {
		t.Fatalf("turn 3 assistant = %q, want remaining detail to survive within window", got)
	}
}

func TestCompactorForRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		manager  ContextManager
		wantType string
	}{
		{
			name:     "no ContextManager defaults to summarize",
			manager:  nil,
			wantType: "summarizeCompactor",
		},
		{
			name:     "NaiveContextManager defaults to summarize",
			manager:  &NaiveContextManager{},
			wantType: "summarizeCompactor",
		},
		{
			name: "SmartContextManager with drop strategy",
			manager: NewContextManager("smart", config.ContextManagementConfig{
				CompactionStrategy: config.CompactionStrategyDrop,
			}),
			wantType: "dropCompactor",
		},
		{
			name: "SmartContextManager with hybrid strategy",
			manager: NewContextManager("smart", config.ContextManagementConfig{
				CompactionStrategy: config.CompactionStrategyHybrid,
			}),
			wantType: "hybridCompactor",
		},
		{
			name: "SmartContextManager with summarize strategy",
			manager: NewContextManager("smart", config.ContextManagementConfig{
				CompactionStrategy: config.CompactionStrategySummarize,
			}),
			wantType: "summarizeCompactor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := RunRequest{ContextManager: tt.manager}
			compactor := compactorForRequest(req)
			switch tt.wantType {
			case "summarizeCompactor":
				if _, ok := compactor.(summarizeCompactor); !ok {
					t.Fatalf("compactorForRequest() type = %T, want summarizeCompactor", compactor)
				}
			case "dropCompactor":
				if _, ok := compactor.(dropCompactor); !ok {
					t.Fatalf("compactorForRequest() type = %T, want dropCompactor", compactor)
				}
			case "hybridCompactor":
				if _, ok := compactor.(hybridCompactor); !ok {
					t.Fatalf("compactorForRequest() type = %T, want hybridCompactor", compactor)
				}
			default:
				t.Fatalf("unknown wantType %q", tt.wantType)
			}
		})
	}
}
