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
				"Use `advisor` as an in-loop check",
				"A final advisor sanity check is mandatory",
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
				"### Step Sizing",
				"safe fix mode",
				"For each finding or concern the advisor raises",
				"explicitly either",
				"Apply",
				"the finding",
				"Reject",
				"state the specific reason",
				"Do not advance to commit without explicitly addressing every material finding",
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
				"Work directly in the shared worktree",
				"No user-approval gates.",
				"No clarifying questions.",
				"Use `advisor` as a point consult",
				"Commit validated units as you complete them.",
				"Do not leave proven work uncommitted.",
				"## Sequence",
				"### Implementation code restriction",
				"You MUST NOT call file-mutation tools",
				"Doing it directly is a violation, not a fallback",
				"There is no inline execution tier",
				"## Execution Artifact",
				"execution.md",
				"plan.yaml",
				"no_delegate",
				"resuming this phase after a prior failure",
				"git commit log",
				"prior progress",
				"Closed rationalization loopholes",
				"low ambiguity",
				"small testable chunks",
				"cheap-feeling mutate calls",
				"only exemptions are the executor-owned artifacts",
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
				"Drive blocking findings to green",
				"Use `advisor` as a loop driver",
				"A final advisor sanity check is mandatory",
				"## Sequence",
				"## Review Standard",
				"## Findings",
				"pass_with_notes",
				"informational",
				"## Review-Fix Loop",
				"You MUST NOT call file-mutation tools",
				"Doing it directly is a violation, not a fallback",
				"## Advisor Sanity Check",
				"you MUST call `advisor` once as a final sanity check",
				"## Review Artifact",
				"review.md",
				"Every interim and final review status response",
				"all currently known blocking and non-blocking findings",
				"including when the status is `fail`",
				"must not silently omit findings reported earlier",
				"could not run because of the environment",
				"verification gaps",
				"local-only branch",
				"closeout notes",
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
