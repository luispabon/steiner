package prompt

import (
	"context"
	"testing"
)

func TestPlanSourceAssemblyOrdersSources(t *testing.T) {
	t.Parallel()

	plan := (Assembler{}).planSourceAssembly()

	want := []plannedSourceKind{
		plannedSourcePreamble,
		plannedSourceAgents,
		plannedSourceProjectContext,
		plannedSourceSkills,
		plannedSourceDurableContext,
		plannedSourceConversation,
		plannedSourceToolSummaries,
	}

	if got, wantLen := len(plan.Steps), len(want); got != wantLen {
		t.Fatalf("len(plan.Steps) = %d, want %d", got, wantLen)
	}
	for i, wantKind := range want {
		if got := plan.Steps[i].Kind; got != wantKind {
			t.Fatalf("plan.Steps[%d].Kind = %q, want %q", i, got, wantKind)
		}
		if plan.Steps[i].Apply == nil {
			t.Fatalf("plan.Steps[%d].Apply = nil, want executable step", i)
		}
	}
}

func TestPlanSourceAssemblyMarksConversationAndToolSummaryPlacement(t *testing.T) {
	t.Parallel()

	plan := (Assembler{}).planSourceAssembly()

	conv := plan.Steps[5]
	if got, want := conv.Kind, plannedSourceConversation; got != want {
		t.Fatalf("conversation step kind = %q, want %q", got, want)
	}
	if !conv.PassThrough {
		t.Fatalf("conversation step PassThrough = false, want true")
	}
	if got, want := conv.Placement, plannedSourcePlacementConversation; got != want {
		t.Fatalf("conversation step placement = %q, want %q", got, want)
	}

	toolSummaries := plan.Steps[6]
	if got, want := toolSummaries.Kind, plannedSourceToolSummaries; got != want {
		t.Fatalf("tool summary step kind = %q, want %q", got, want)
	}
	if toolSummaries.PassThrough {
		t.Fatalf("tool summary step PassThrough = true, want false")
	}
	if got, want := toolSummaries.Placement, plannedSourcePlacementToolSummaries; got != want {
		t.Fatalf("tool summary step placement = %q, want %q", got, want)
	}
}

func TestPlanSourceAssemblyIsBudgetIndependent(t *testing.T) {
	t.Parallel()

	lowBudgetAssembler := Assembler{
		opts: AssemblyOptions{
			Policy: AssemblyPolicy{
				Budgets: SourceBudgetModel{
					PreambleBytes:       1,
					GlobalAgentsBytes:   1,
					ProjectAgentsBytes:  1,
					ProjectContextBytes: 1,
					SkillBytes:          1,
					DurableContextBytes: 1,
					ToolResultBytes:     1,
					ToolSummaryBytes:    1,
				},
			},
		},
	}
	highBudgetAssembler := Assembler{
		opts: AssemblyOptions{
			Policy: AssemblyPolicy{
				Budgets: SourceBudgetModel{
					PreambleBytes:       1024,
					GlobalAgentsBytes:   2048,
					ProjectAgentsBytes:  8192,
					ProjectContextBytes: 4096,
					SkillBytes:          2048,
					DurableContextBytes: 1024,
					ToolResultBytes:     2048,
					ToolSummaryBytes:    1024,
				},
			},
		},
	}

	lowPlan := lowBudgetAssembler.planSourceAssembly()
	highPlan := highBudgetAssembler.planSourceAssembly()

	if got, want := len(lowPlan.Steps), len(highPlan.Steps); got != want {
		t.Fatalf("plan step count differs: low=%d high=%d", got, want)
	}
	for i := range lowPlan.Steps {
		if got, want := lowPlan.Steps[i].Kind, highPlan.Steps[i].Kind; got != want {
			t.Fatalf("plan step %d kind = %q, want %q", i, got, want)
		}
		if lowPlan.Steps[i].Apply == nil || highPlan.Steps[i].Apply == nil {
			t.Fatalf("plan step %d apply unexpectedly nil", i)
		}
	}
}

func TestRenderSourcePlanMatchesAssemble(t *testing.T) {
	t.Parallel()

	assembler, err := newAssembler(AssemblyOptions{})
	if err != nil {
		t.Fatalf("newAssembler() error = %v", err)
	}

	assembly, err := assembler.renderSourcePlan(context.Background(), assembler.planSourceAssembly())
	if err != nil {
		t.Fatalf("renderSourcePlan() error = %v", err)
	}

	got, err := assembler.Assemble(context.Background())
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	if gotLen, wantLen := len(assembly.Blocks), len(got.Blocks); gotLen != wantLen {
		t.Fatalf("rendered blocks = %d, want %d", gotLen, wantLen)
	}
	if gotLen, wantLen := len(assembly.Messages), len(got.Messages); gotLen != wantLen {
		t.Fatalf("rendered messages = %d, want %d", gotLen, wantLen)
	}
}
