package agent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

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
		Provider:      providerStub,
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         100000,
			MaxCompletionTokens: 256,
			SafetyMarginTokens:  0,
			SummaryMaxTokens:    128,
		},
	}
	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 3, candidate)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if !outcome.Applied {
		t.Fatal("Applied = false, want true")
	}
	if got, want := len(providerStub.requests), 1; got != want {
		t.Fatalf("ChatCompletion calls = %d, want %d", got, want)
	}
	if got, want := outcome.State.Conversation[0].Role, MessageRoleSummary; got != want {
		t.Fatalf("conversation[0].role = %q, want %q", got, want)
	}
	for _, needle := range []string{"turn 2 user", "turn 2 assistant"} {
		if !messageContentsContain(ToProviderMessages(outcome.State.Conversation), needle) {
			t.Fatalf("conversation missing retained content %q: %#v", needle, outcome.State.Conversation)
		}
	}
	if messageContentsContain(ToProviderMessages(outcome.State.Conversation), "turn 1 user") {
		t.Fatalf("conversation retained older turn in raw transcript: %#v", outcome.State.Conversation)
	}
	if got, want := len(outcome.State.Context.RetainedSummaries), 1; got != want {
		t.Fatalf("retained summaries = %d, want %d", got, want)
	}
	if got, want := outcome.State.Context.RetainedSummaries[0].Text, "summary handoff text"; got != want {
		t.Fatalf("retained summary text = %q, want %q", got, want)
	}
}

func TestSummarizeCompactorCutsSourceBeforeRecentTurns(t *testing.T) {
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

	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}

	req := RunRequest{
		Provider:      providerStub,
		Tools:         []provider.ToolSpec{{Type: "function", Function: provider.ToolFunctionSpec{Name: "read"}}},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         100000,
			MaxCompletionTokens: 256,
			SafetyMarginTokens:  0,
			SummaryMaxTokens:    128,
		},
	}
	sourceMessages := []Message{
		{Role: MessageRoleUser, Content: "turn 1 user"},
		{Role: MessageRoleAssistant, Content: "turn 1 assistant"},
	}
	expectedAssembly, err := prompt.Assemble(context.Background(), assemblyOptions(prepareBasePrompt(req), state.WithConversation(sourceMessages)))
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 4, candidate)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if !outcome.Applied {
		t.Fatal("Applied = false, want true")
	}
	if got, want := len(providerStub.requests), 1; got != want {
		t.Fatalf("ChatCompletion calls = %d, want %d", got, want)
	}
	promptMessages := providerStub.requests[0].Messages
	// The compaction request must replay the same tools as a normal turn so it
	// reuses the identical cached prefix (system + tools + conversation) and hits
	// the prompt cache, rather than dropping tools and letting any ExtraParams
	// tools leak through unfiltered.
	if got, want := providerStub.requests[0].Tools, req.Tools; !reflect.DeepEqual(got, want) {
		t.Fatalf("compaction request tools = %#v, want same as normal turn %#v", got, want)
	}
	if len(promptMessages) != len(expectedAssembly.Messages)+1 {
		t.Fatalf("compaction prompt messages = %d, want assembled prefix %d plus final instruction", len(promptMessages), len(expectedAssembly.Messages))
	}
	if got := promptMessages[:len(expectedAssembly.Messages)]; !reflect.DeepEqual(got, expectedAssembly.Messages) {
		t.Fatalf("compaction prompt prefix = %#v, want normal assembly %#v", got, expectedAssembly.Messages)
	}
	finalMessage := promptMessages[len(promptMessages)-1]
	if got, want := finalMessage.Role, provider.MessageRoleUser; got != want {
		t.Fatalf("final compaction message role = %q, want %q", got, want)
	}
	if got := finalMessage.Content; !strings.Contains(got, "You are compacting the current working context for a coding agent.") {
		t.Fatalf("final compaction message = %q, want compaction instruction", got)
	}
	if !messageContentsContain(promptMessages, "turn 1 user") {
		t.Fatal("compaction prompt missing older history")
	}
	if messageContentsContain(promptMessages, "turn 1: user: turn 1 user") {
		t.Fatal("compaction prompt summarized source as turn transcript, want verbatim assembled messages")
	}
	for _, needle := range []string{"turn 2 user", "turn 3 user", "turn 4 user"} {
		if messageContentsContain(promptMessages, needle) {
			t.Fatalf("compaction prompt retained %q, want it excluded from the summary source", needle)
		}
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
	if got, want := outcome.State.Conversation[0].Role, MessageRoleSummary; got != want {
		t.Fatalf("conversation[0].role = %q, want %q", got, want)
	}
	for _, needle := range []string{"post-compaction user", "post-compaction assistant"} {
		if !messageContentsContain(ToProviderMessages(outcome.State.Conversation), needle) {
			t.Fatalf("conversation missing retained content %q: %#v", needle, outcome.State.Conversation)
		}
	}
}

func TestSummarizeCompactorSkipsEmptyNormalStageAndUsesEmergencySource(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "emergency handoff summary",
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
			{Role: MessageRoleUser, Content: "turn 3 user"},
			{Role: MessageRoleAssistant, Content: "turn 3 assistant"},
		},
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleUser, Content: "turn 1 user"},
			{Role: MessageRoleAssistant, Content: "turn 1 assistant"},
			{Role: MessageRoleUser, Content: "turn 2 user"},
			{Role: MessageRoleAssistant, Content: "turn 2 assistant"},
			{Role: MessageRoleUser, Content: "turn 3 user"},
			{Role: MessageRoleAssistant, Content: "turn 3 assistant"},
		}),
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
				ContextSize:               100000,
				MaxCompletionTokens:       256,
				SafetyMarginTokens:        0,
				SummaryMaxTokens:          128,
				NormalSummaryMaxTokens:    128,
				EmergencySummaryMaxTokens: 128,
			},
		},
		state,
		4,
		candidate,
	)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if !outcome.Applied {
		t.Fatal("Applied = false, want true")
	}
	if got, want := outcome.Mode, prompt.CompactionModeEmergency; got != want {
		t.Fatalf("Mode = %q, want %q", got, want)
	}
	if got, want := len(providerStub.requests), 1; got != want {
		t.Fatalf("ChatCompletion calls = %d, want %d", got, want)
	}
	promptMessages := providerStub.requests[0].Messages
	if !messageContentsContain(promptMessages, "turn 1 user") || !messageContentsContain(promptMessages, "turn 2 user") {
		t.Fatalf("compaction prompt = %#v, want emergency source to include older turns", promptMessages)
	}
	if messageContentsContain(promptMessages, "turn 3 user") {
		t.Fatal("compaction prompt retained latest turn, want it excluded from emergency source")
	}
}

func TestSummarizeCompactorRunsEmergencyStageWhenNormalCompactionLeavesPromptTooDense(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: strings.Repeat("normal summary detail ", 600),
				},
				FinishReason: "stop",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "emergency handoff summary",
				},
				FinishReason: "stop",
			},
		},
	}

	state := RunState{
		Conversation: []Message{
			{Role: MessageRoleUser, Content: strings.Repeat("turn 1 user detail ", 4)},
			{Role: MessageRoleAssistant, Content: strings.Repeat("turn 1 assistant detail ", 4)},
			{Role: MessageRoleUser, Content: strings.Repeat("turn 2 user detail ", 4)},
			{Role: MessageRoleAssistant, Content: strings.Repeat("turn 2 assistant detail ", 4)},
			{Role: MessageRoleUser, Content: strings.Repeat("turn 3 user detail ", 4)},
			{Role: MessageRoleAssistant, Content: strings.Repeat("turn 3 assistant detail ", 4)},
			{Role: MessageRoleUser, Content: strings.Repeat("turn 4 user detail ", 4)},
			{Role: MessageRoleAssistant, Content: strings.Repeat("turn 4 assistant detail ", 4)},
		},
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleUser, Content: strings.Repeat("turn 1 user detail ", 4)},
			{Role: MessageRoleAssistant, Content: strings.Repeat("turn 1 assistant detail ", 4)},
			{Role: MessageRoleUser, Content: strings.Repeat("turn 2 user detail ", 4)},
			{Role: MessageRoleAssistant, Content: strings.Repeat("turn 2 assistant detail ", 4)},
			{Role: MessageRoleUser, Content: strings.Repeat("turn 3 user detail ", 4)},
			{Role: MessageRoleAssistant, Content: strings.Repeat("turn 3 assistant detail ", 4)},
			{Role: MessageRoleUser, Content: strings.Repeat("turn 4 user detail ", 4)},
			{Role: MessageRoleAssistant, Content: strings.Repeat("turn 4 assistant detail ", 4)},
		}),
	}

	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}

	req := RunRequest{
		Provider:      providerStub,
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:               3300,
			MaxCompletionTokens:       128,
			SafetyMarginTokens:        0,
			SummaryMaxTokens:          64,
			NormalSummaryMaxTokens:    64,
			EmergencySummaryMaxTokens: 16,
		},
	}

	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 5, candidate)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if !outcome.Applied {
		t.Fatal("Applied = false, want true")
	}
	if got, want := len(providerStub.requests), 2; got != want {
		t.Fatalf("ChatCompletion calls = %d, want %d", got, want)
	}
	if got, want := *providerStub.requests[0].MaxTokens, 64; got != want {
		t.Fatalf("normal compaction max_tokens = %d, want %d", got, want)
	}
	if got, want := *providerStub.requests[1].MaxTokens, 16; got != want {
		t.Fatalf("emergency compaction max_tokens = %d, want %d", got, want)
	}
	emergencyMessages := providerStub.requests[1].Messages
	emergencyInstruction := emergencyMessages[len(emergencyMessages)-1]
	if got, want := emergencyInstruction.Role, provider.MessageRoleUser; got != want {
		t.Fatalf("emergency instruction role = %q, want %q", got, want)
	}
	if got := emergencyInstruction.Content; !strings.Contains(got, "emergency handoff") {
		t.Fatalf("emergency instruction = %q, want emergency handoff instruction", got)
	}
}

func TestTwoStageSummarizeCompactionErrorsWhenEmergencyStageDoesNotApply(t *testing.T) {
	state := RunState{
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

	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}

	normalState := RunState{
		Conversation: []Message{{Role: MessageRoleSummary, Content: "normal summary"}},
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleSummary, Content: "normal summary"},
		}),
	}

	req := RunRequest{
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:               1000,
			MaxCompletionTokens:       128,
			SafetyMarginTokens:        0,
			SummaryMaxTokens:          64,
			NormalSummaryMaxTokens:    64,
			EmergencySummaryMaxTokens: 16,
		},
	}

	outcome, err := summarizeCompactionStages{
		stageRunner: func(_ context.Context, _ RunRequest, _ RunState, _ int, _ ConversationCandidate, _ []Message, _ []Message, mode prompt.CompactionMode, _ int) (CompactionOutcome, error) {
			switch mode {
			case prompt.CompactionModeNormal:
				return CompactionOutcome{
					Applied:   true,
					Candidate: candidate,
					State:     normalState,
					Fit: prompt.RequestTokenBudget{
						EstimatedPromptTokens: 680,
						ContextSize:           1000,
						PromptUsage:           0.80,
					},
				}, nil
			case prompt.CompactionModeEmergency:
				return CompactionOutcome{
					Applied:   false,
					Candidate: candidate,
					Fit: prompt.RequestTokenBudget{
						EstimatedPromptTokens: 640,
						ContextSize:           900,
						PromptUsage:           0.711,
					},
				}, nil
			default:
				t.Fatalf("unexpected mode %q", mode)
				return CompactionOutcome{}, nil
			}
		},
		fitRunner: func(context.Context, RunRequest, RunState) (prompt.RequestTokenBudget, error) {
			return prompt.RequestTokenBudget{
				EstimatedPromptTokens: 680,
				ContextSize:           1000,
				PromptUsage:           0.80,
			}, nil
		},
	}.run(
		context.Background(),
		req,
		state,
		5,
		candidate,
	)
	if err == nil {
		t.Fatal("summarizeCompactionStages.run() error = nil, want non-nil")
	}
	for _, needle := range []string{
		"emergency compaction could not reduce context enough",
		"prompt=",
		"context_window=",
		"usage=",
		"compaction_threshold=70%",
	} {
		if !strings.Contains(err.Error(), needle) {
			t.Fatalf("error = %q, want %q", err.Error(), needle)
		}
	}
	if outcome.Applied {
		t.Fatal("Applied = true, want false from unapplied emergency stage")
	}
}

func TestSummarizeCompactorFailsWhenEmergencyTailCannotFit(t *testing.T) {
	providerStub := &fakeProvider{}

	state := RunState{
		Conversation: []Message{
			{Role: MessageRoleUser, Content: strings.Repeat("latest user content ", 10)},
			{Role: MessageRoleAssistant, Content: strings.Repeat("latest assistant content ", 10)},
		},
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleUser, Content: strings.Repeat("latest user content ", 10)},
			{Role: MessageRoleAssistant, Content: strings.Repeat("latest assistant content ", 10)},
		}),
	}

	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}

	req := RunRequest{
		Provider:      providerStub,
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:               16,
			MaxCompletionTokens:       16,
			SafetyMarginTokens:        0,
			SummaryMaxTokens:          8,
			NormalSummaryMaxTokens:    8,
			EmergencySummaryMaxTokens: 8,
		},
	}

	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 1, candidate)
	if err == nil {
		t.Fatal("Compact() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "compaction cannot solve this request") {
		t.Fatalf("error = %q, want compaction cannot solve message", err.Error())
	}
	if outcome.Applied {
		t.Fatal("Applied = true, want false")
	}
	if got := len(providerStub.requests); got != 0 {
		t.Fatalf("provider calls = %d, want 0 when retained tail cannot fit", got)
	}
}

func TestSummarizeCompactorErrorsWhenEmergencyCompactionCannotReduceEnough(t *testing.T) {
	err := emergencyCompactionError(prompt.RequestTokenBudget{
		EstimatedPromptTokens: 640,
		ContextSize:           900,
		PromptUsage:           0.711,
	})
	if err == nil {
		t.Fatal("emergencyCompactionError() error = nil, want non-nil")
	}
	for _, needle := range []string{
		"emergency compaction could not reduce context enough",
		"prompt=",
		"context_window=",
		"usage=",
		"compaction_threshold=70%",
	} {
		if !strings.Contains(err.Error(), needle) {
			t.Fatalf("error = %q, want %q", err.Error(), needle)
		}
	}
}

func TestCompactionSourceAndRetentionUsesNormalAndEmergencyWindows(t *testing.T) {
	messages := []Message{
		{Role: MessageRoleUser, Content: "turn 1 user"},
		{Role: MessageRoleAssistant, Content: "turn 1 assistant"},
		{Role: MessageRoleTool, Name: "read", ToolCallID: "call-1", Content: "turn 1 tool output"},
		{Role: MessageRoleUser, Content: "turn 2 user"},
		{Role: MessageRoleAssistant, Content: "turn 2 assistant"},
		{Role: MessageRoleUser, Content: "turn 3 user"},
		{Role: MessageRoleAssistant, Content: "turn 3 assistant"},
		{Role: MessageRoleUser, Content: "turn 4 user"},
		{Role: MessageRoleAssistant, Content: "turn 4 assistant"},
		{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-4", Content: "turn 4 tool output"},
		{Role: MessageRoleUser, Content: "turn 5 user"},
		{Role: MessageRoleAssistant, Content: "turn 5 assistant"},
	}

	normalSource, normalRetained := compactionSourceAndRetention(messages, messages, normalCompactionRetainTurns)
	if got, want := len(normalRetained), 7; got != want {
		t.Fatalf("normal retained len = %d, want %d", got, want)
	}
	if got, want := len(normalSource), 5; got != want {
		t.Fatalf("normal source len = %d, want %d", got, want)
	}
	if got, want := normalRetained[0].Content, "turn 3 user"; got != want {
		t.Fatalf("normal retained[0] = %q, want %q", got, want)
	}
	if got, want := normalRetained[len(normalRetained)-2].Content, "turn 5 user"; got != want {
		t.Fatalf("normal latest user = %q, want %q", got, want)
	}
	if !messageContentsContain(ToProviderMessages(normalRetained), "turn 4 tool output") {
		t.Fatal("normal retained messages missing recent tool output, want raw recent turn preserved")
	}
	if messageContentsContain(ToProviderMessages(normalRetained), "turn 1 tool output") {
		t.Fatal("normal retained messages kept old tool output, want it outside retained window")
	}

	emergencySource, emergencyRetained := compactionSourceAndRetention(messages, messages, emergencyCompactionRetainTurns)
	if got, want := len(emergencyRetained), 2; got != want {
		t.Fatalf("emergency retained len = %d, want %d", got, want)
	}
	if got, want := len(emergencySource), 10; got != want {
		t.Fatalf("emergency source len = %d, want %d", got, want)
	}
	if got, want := emergencyRetained[0].Content, "turn 5 user"; got != want {
		t.Fatalf("emergency retained[0] = %q, want %q", got, want)
	}
	if !messageContentsContain(ToProviderMessages(emergencyRetained), "turn 5 user") {
		t.Fatal("emergency retained messages missing latest user turn, want raw latest user preserved")
	}
}

func TestTruncateCompactionMessagesShortensLongToolOutputs(t *testing.T) {
	messages := []Message{
		{Role: MessageRoleTool, Name: "read", ToolCallID: "call-1", Content: strings.Repeat("tool output ", 18)},
		{Role: MessageRoleAssistant, Content: "short assistant"},
	}

	truncated := truncateCompactionMessages(messages, 24)
	if len(truncated) != len(messages) {
		t.Fatalf("truncateCompactionMessages len = %d, want %d", len(truncated), len(messages))
	}
	if len(truncated[0].Content) >= len(messages[0].Content) {
		t.Fatalf("truncated tool output length = %d, want shorter than %d", len(truncated[0].Content), len(messages[0].Content))
	}
	if !strings.HasSuffix(truncated[0].Content, "...") {
		t.Fatalf("truncated tool output = %q, want ellipsis suffix", truncated[0].Content)
	}
	if got, want := truncated[1].Content, "short assistant"; got != want {
		t.Fatalf("non-truncated message = %q, want %q", got, want)
	}
}

func TestSummarizeCompactorRetainsRecentTurnsAndDropsOlderToolOutput(t *testing.T) {
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
			{Role: MessageRoleTool, Name: "read", ToolCallID: "call-1", Content: strings.Repeat("turn 1 tool output ", 12)},
			{Role: MessageRoleUser, Content: "turn 2 user"},
			{Role: MessageRoleAssistant, Content: "turn 2 assistant"},
			{Role: MessageRoleUser, Content: "turn 3 user"},
			{Role: MessageRoleAssistant, Content: "turn 3 assistant"},
			{Role: MessageRoleUser, Content: "turn 4 user"},
			{Role: MessageRoleAssistant, Content: "turn 4 assistant"},
			{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-4", Content: "turn 4 tool output"},
			{Role: MessageRoleUser, Content: "turn 5 user"},
			{Role: MessageRoleAssistant, Content: "turn 5 assistant"},
		},
		Lineage: newConversationLineage([]Message{
			{Role: MessageRoleUser, Content: "turn 1 user"},
			{Role: MessageRoleAssistant, Content: "turn 1 assistant"},
			{Role: MessageRoleTool, Name: "read", ToolCallID: "call-1", Content: strings.Repeat("turn 1 tool output ", 12)},
			{Role: MessageRoleUser, Content: "turn 2 user"},
			{Role: MessageRoleAssistant, Content: "turn 2 assistant"},
			{Role: MessageRoleUser, Content: "turn 3 user"},
			{Role: MessageRoleAssistant, Content: "turn 3 assistant"},
			{Role: MessageRoleUser, Content: "turn 4 user"},
			{Role: MessageRoleAssistant, Content: "turn 4 assistant"},
			{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-4", Content: "turn 4 tool output"},
			{Role: MessageRoleUser, Content: "turn 5 user"},
			{Role: MessageRoleAssistant, Content: "turn 5 assistant"},
		}),
	}

	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}

	req := RunRequest{
		Provider:      providerStub,
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:               100000,
			MaxCompletionTokens:       256,
			SafetyMarginTokens:        0,
			SummaryMaxTokens:          128,
			NormalSummaryMaxTokens:    128,
			EmergencySummaryMaxTokens: 64,
		},
	}

	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 5, candidate)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if !outcome.Applied {
		t.Fatal("Applied = false, want true")
	}
	if got, want := len(providerStub.requests), 1; got != want {
		t.Fatalf("ChatCompletion calls = %d, want %d", got, want)
	}
	conversation := ToProviderMessages(outcome.State.Conversation)
	if !messageContentsContain(conversation, "turn 5 user") {
		t.Fatal("compacted conversation missing latest user turn, want raw latest user preserved")
	}
	if !messageContentsContain(conversation, "turn 4 tool output") {
		t.Fatal("compacted conversation missing recent tool output, want retained raw recent turn")
	}
	if messageContentsContain(conversation, "turn 1 tool output") {
		t.Fatal("compacted conversation kept older tool output, want it dropped from retained window")
	}
}

func TestSummarizeCompactorUsesBaselinePath(t *testing.T) {
	t.Parallel()

	if _, ok := any(summarizeCompactor{}).(Compactor); !ok {
		t.Fatal("summarizeCompactor does not implement Compactor")
	}
}

func TestStripImages_DropImagesFromMessages(t *testing.T) {
	t.Parallel()

	messages := []Message{
		{
			Role:    MessageRoleUser,
			Content: "Check this image",
			Images: []ImageBlock{
				{
					MediaType: "image/png",
					Data:      "iVBORw0KGgo=",
					Width:     100,
					Height:    100,
					SizeBytes: 1024,
				},
			},
		},
		{
			Role:    MessageRoleAssistant,
			Content: "I see the image",
			Images: []ImageBlock{
				{
					MediaType: "image/jpeg",
					Data:      "/9j/4AAQ",
					Width:     200,
					Height:    200,
					SizeBytes: 2048,
				},
			},
		},
	}

	result := stripImages(messages)

	if len(result) != 2 {
		t.Fatalf("stripImages len = %d, want 2", len(result))
	}

	for i, msg := range result {
		if msg.Images != nil {
			t.Fatalf("message %d Images = %v, want nil", i, msg.Images)
		}
		if !strings.Contains(msg.Content, "[image was attached]") {
			t.Fatalf("message %d Content = %q, want to contain '[image was attached]'", i, msg.Content)
		}
	}
}

func TestStripImages_PreserveMessagesWithoutImages(t *testing.T) {
	t.Parallel()

	messages := []Message{
		{
			Role:    MessageRoleUser,
			Content: "No images here",
		},
		{
			Role:    MessageRoleAssistant,
			Content: "Also no images",
		},
	}

	result := stripImages(messages)

	if len(result) != 2 {
		t.Fatalf("stripImages len = %d, want 2", len(result))
	}

	if got, want := result[0].Content, "No images here"; got != want {
		t.Fatalf("message 0 Content = %q, want %q", got, want)
	}
	if got, want := result[1].Content, "Also no images"; got != want {
		t.Fatalf("message 1 Content = %q, want %q", got, want)
	}
}

func TestStripImages_AppendMarkerToExistingContent(t *testing.T) {
	t.Parallel()

	messages := []Message{
		{
			Role:    MessageRoleUser,
			Content: "Here is an image",
			Images: []ImageBlock{
				{
					MediaType: "image/png",
					Data:      "iVBORw0KGgo=",
				},
			},
		},
	}

	result := stripImages(messages)

	if len(result) != 1 {
		t.Fatalf("stripImages len = %d, want 1", len(result))
	}

	expected := "Here is an image\n[image was attached]"
	if got := result[0].Content; got != expected {
		t.Fatalf("Content = %q, want %q", got, expected)
	}
	if result[0].Images != nil {
		t.Fatalf("Images = %v, want nil", result[0].Images)
	}
}

func TestStripImages_MarkerOnlyWhenNoContent(t *testing.T) {
	t.Parallel()

	messages := []Message{
		{
			Role: MessageRoleUser,
			Images: []ImageBlock{
				{
					MediaType: "image/png",
					Data:      "iVBORw0KGgo=",
				},
			},
		},
	}

	result := stripImages(messages)

	if len(result) != 1 {
		t.Fatalf("stripImages len = %d, want 1", len(result))
	}

	if got, want := result[0].Content, "[image was attached]"; got != want {
		t.Fatalf("Content = %q, want %q", got, want)
	}
	if result[0].Images != nil {
		t.Fatalf("Images = %v, want nil", result[0].Images)
	}
}

func TestCompleteCompactionCallRecordsUsageOnSuccess(t *testing.T) {
	tests := []struct {
		name         string
		usage        *provider.UsageStats
		wantRecorded bool
	}{
		{
			name: "usage-bearing response records exactly one observation",
			usage: &provider.UsageStats{
				PromptTokens:             120,
				CompletionTokens:         60,
				CacheReadInputTokens:     8,
				CacheCreationInputTokens: 4,
			},
			wantRecorded: true,
		},
		{
			name:         "nil usage records nothing",
			usage:        nil,
			wantRecorded: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prov := &fakeProvider{
				responses: []provider.ChatResponse{
					{
						Message: provider.Message{
							Role:    provider.MessageRoleAssistant,
							Content: "compaction summary",
						},
						Usage:        tc.usage,
						FinishReason: "stop",
					},
				},
			}

			recorder := &testRecorder{}
			_, err := completeCompactionCall(context.Background(), RunRequest{
				Provider: prov,
				ResolvedModel: provider.ResolvedModel{
					ProviderAlias:         "claude-test",
					EffectiveProviderType: "anthropic",
					BackendModelID:        "claude-3-5-sonnet",
				},
				UsageRecorder: recorder,
				Events:        output.SinkFunc(func(output.Event) {}),
			}, 1, provider.ChatRequest{Model: "test"}, prompt.ModelTokenBudget{})

			if err != nil {
				t.Fatalf("completeCompactionCall() error = %v", err)
			}

			recordedCount := len(recorder.obs)
			if tc.wantRecorded && recordedCount != 1 {
				t.Errorf("recorded observations = %d, want 1", recordedCount)
			}
			if !tc.wantRecorded && recordedCount != 0 {
				t.Errorf("recorded observations = %d, want 0", recordedCount)
			}

			if tc.wantRecorded && recordedCount == 1 {
				obs := recorder.obs[0]
				if obs.PromptTokens != 120 {
					t.Errorf("PromptTokens = %d, want 120", obs.PromptTokens)
				}
				if obs.CompletionTokens != 60 {
					t.Errorf("CompletionTokens = %d, want 60", obs.CompletionTokens)
				}
				if obs.CacheReadTokens != 8 {
					t.Errorf("CacheReadTokens = %d, want 8", obs.CacheReadTokens)
				}
				if obs.CacheCreateTokens != 4 {
					t.Errorf("CacheCreateTokens = %d, want 4", obs.CacheCreateTokens)
				}
				if obs.ProviderAlias != "claude-test" {
					t.Errorf("ProviderAlias = %q, want %q", obs.ProviderAlias, "claude-test")
				}
				if obs.BackendModelID != "claude-3-5-sonnet" {
					t.Errorf("BackendModelID = %q, want %q", obs.BackendModelID, "claude-3-5-sonnet")
				}
			}
		})
	}
}

func TestCompleteCompactionCallDoesNotRecordOnError(t *testing.T) {
	boom := fmt.Errorf("api error")
	prov := &fakeProvider{
		chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
			return provider.ChatResponse{}, boom
		},
	}

	recorder := &testRecorder{}
	_, err := completeCompactionCall(context.Background(), RunRequest{
		Provider: prov,
		ResolvedModel: provider.ResolvedModel{
			ProviderAlias:         "claude-test",
			EffectiveProviderType: "anthropic",
			BackendModelID:        "claude-3-5-sonnet",
		},
		UsageRecorder: recorder,
		Events:        output.SinkFunc(func(output.Event) {}),
	}, 1, provider.ChatRequest{Model: "test"}, prompt.ModelTokenBudget{})

	if !errors.Is(err, boom) {
		t.Fatalf("completeCompactionCall() error = %v, want %v", err, boom)
	}

	if got := len(recorder.obs); got != 0 {
		t.Errorf("recorded observations = %d, want 0 on error", got)
	}
}
