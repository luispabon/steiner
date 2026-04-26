package agent

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
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
