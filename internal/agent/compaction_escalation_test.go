package agent

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
)

func TestCompactionCannotSolveErrorReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		fit        prompt.RequestTokenBudget
		wantReason string
	}{
		{
			name: "prompt over hard limit",
			fit: prompt.RequestTokenBudget{
				EstimatedPromptTokens:    90000,
				ReservedCompletionTokens: 4096,
				SafetyMarginTokens:       2048,
				TotalTokens:              96144,
				ContextSize:              100000,
				PromptUsage:              0.9,
				HardLimitTokens:          80000,
			},
			wantReason: "retained conversation already exceeds the hard context limit",
		},
		{
			name: "prompt fits but reserve does not",
			fit: prompt.RequestTokenBudget{
				EstimatedPromptTokens:    78000,
				ReservedCompletionTokens: 20000,
				SafetyMarginTokens:       2048,
				TotalTokens:              100048,
				ContextSize:              100000,
				PromptUsage:              0.78,
				HardLimitTokens:          80000,
			},
			wantReason: "plus the completion reserve exceeds the context window",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := compactionCannotSolveError(tt.fit)
			if err == nil {
				t.Fatal("compactionCannotSolveError() = nil, want error")
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, "compaction cannot solve this request") {
				t.Fatalf("error = %q, want prefix %q", msg, "compaction cannot solve this request")
			}
			if !strings.Contains(msg, tt.wantReason) {
				t.Fatalf("error = %q, want reason %q", msg, tt.wantReason)
			}
			for _, want := range []string{"reserve=", "safety_margin=", "total=", "hard_limit="} {
				if !strings.Contains(msg, want) {
					t.Fatalf("error = %q, want substring %q", msg, want)
				}
			}
		})
	}
}
