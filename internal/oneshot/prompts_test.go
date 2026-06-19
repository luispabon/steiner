package oneshot

import (
	"strings"
	"testing"
)

func TestLoadPrompt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		phase   Phase
		wantIn  []string
		wantOut []string
	}{
		{
			name:  "plan",
			phase: PhasePlan,
			wantIn: []string{
				"orchestrator-internal plan phase",
				"not by a user slash-command skill",
				"No user-approval gates.",
				"No clarifying questions.",
				"bounded assumption",
				"Use `advisor` as a loop driver",
				"commit-oriented",
				"validated units",
				"Research Decision",
				"Research is required by default",
				"`research` tool",
				"overview.md",
				"plan.yaml",
				"## Decision Log",
				"## Advisor Sanity Check",
				"you MUST call `advisor` once as a final sanity check",
			},
			wantOut: []string{
				"ask the user",
				"wait for approval",
				"user approval required",
			},
		},
		{
			name:  "implement",
			phase: PhaseImplement,
			wantIn: []string{
				"orchestrator-internal implement phase",
				"Work directly in the shared worktree.",
				"No user-approval gates.",
				"No clarifying questions.",
				"Use `advisor` as a point consult",
				"Commit validated units as you complete them.",
				"Do not leave proven work uncommitted.",
			},
			wantOut: []string{
				"ask the user",
				"wait for approval",
				"user approval required",
			},
		},
		{
			name:  "review",
			phase: PhaseReview,
			wantIn: []string{
				"orchestrator-internal review phase",
				"Drive both blocking and non-blocking findings to green",
				"Use `advisor` as a loop driver",
				"advisor sign-off is recorded",
				"blocking findings",
				"non-blocking findings",
			},
			wantOut: []string{
				"ask the user",
				"wait for approval",
				"user approval required",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := LoadPrompt(tt.phase)
			if err != nil {
				t.Fatalf("LoadPrompt(%q) failed: %v", tt.phase, err)
			}
			if strings.TrimSpace(got) == "" {
				t.Fatalf("LoadPrompt(%q) returned empty content", tt.phase)
			}

			for _, want := range tt.wantIn {
				if !strings.Contains(got, want) {
					t.Fatalf("LoadPrompt(%q) missing %q in %q", tt.phase, want, got)
				}
			}
			for _, forbidden := range tt.wantOut {
				if strings.Contains(strings.ToLower(got), strings.ToLower(forbidden)) {
					t.Fatalf("LoadPrompt(%q) unexpectedly contains %q in %q", tt.phase, forbidden, got)
				}
			}
		})
	}
}

func TestLoadPromptRejectsUnknownPhase(t *testing.T) {
	t.Parallel()

	if _, err := LoadPrompt(Phase("unknown")); err == nil {
		t.Fatal("LoadPrompt(unknown) expected error")
	}
}

func TestLoadPromptDistinctByPhase(t *testing.T) {
	t.Parallel()

	plan, err := LoadPrompt(PhasePlan)
	if err != nil {
		t.Fatalf("LoadPrompt(plan) failed: %v", err)
	}
	implement, err := LoadPrompt(PhaseImplement)
	if err != nil {
		t.Fatalf("LoadPrompt(implement) failed: %v", err)
	}
	review, err := LoadPrompt(PhaseReview)
	if err != nil {
		t.Fatalf("LoadPrompt(review) failed: %v", err)
	}

	if plan == implement || plan == review || implement == review {
		t.Fatalf("prompt contents should differ by phase: plan=%q implement=%q review=%q", plan, implement, review)
	}
}
