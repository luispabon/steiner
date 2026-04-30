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
