package prompt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestPlanSourceAssemblyOrdersSources(t *testing.T) {
	t.Parallel()

	plan := (assembler{}).planSourceAssembly()

	want := []sourcePlanStep{
		{Kind: plannedSourcePreamble, Placement: plannedSourcePlacementCore, PassThrough: false},
		{Kind: plannedSourceAgents, Placement: plannedSourcePlacementCore, PassThrough: false},
		{Kind: plannedSourceProjectContext, Placement: plannedSourcePlacementCore, PassThrough: false},
		{Kind: plannedSourceSkills, Placement: plannedSourcePlacementCore, PassThrough: false},
		{Kind: plannedSourceConversation, Placement: plannedSourcePlacementConversation, PassThrough: true},
		{Kind: plannedSourceToolSummaries, Placement: plannedSourcePlacementToolSummaries, PassThrough: false},
	}

	if got, wantLen := len(plan.Steps), len(want); got != wantLen {
		t.Fatalf("len(plan.Steps) = %d, want %d", got, wantLen)
	}
	for i, wantStep := range want {
		got := plan.Steps[i]
		if got.Kind != wantStep.Kind {
			t.Fatalf("plan.Steps[%d].Kind = %q, want %q", i, got.Kind, wantStep.Kind)
		}
		if got.Placement != wantStep.Placement {
			t.Fatalf("plan.Steps[%d].Placement = %q, want %q", i, got.Placement, wantStep.Placement)
		}
		if got.PassThrough != wantStep.PassThrough {
			t.Fatalf("plan.Steps[%d].PassThrough = %t, want %t", i, got.PassThrough, wantStep.PassThrough)
		}
		if got.Apply == nil {
			t.Fatalf("plan.Steps[%d].Apply = nil, want executable step", i)
		}
	}
}

func TestPlanSourceAssemblyExcludesAbsentOptionalSources(t *testing.T) {
	t.Parallel()

	assembly := mustRenderPlannedAssembly(t, AssemblyOptions{})

	if got, want := blockSources(assembly.Blocks), []ContextSource{ContextSourcePreamble}; !sourcesEqual(got, want) {
		t.Fatalf("block sources = %v, want %v", got, want)
	}
	if got, want := len(assembly.Messages), 1; got != want {
		t.Fatalf("len(messages) = %d, want %d", got, want)
	}
	if got, want := assembly.Messages[0].Role, provider.MessageRoleSystem; got != want {
		t.Fatalf("message[0].role = %q, want %q", got, want)
	}
	if got := assembly.Messages[0].Content; !strings.HasPrefix(SystemPreamble("", false, false, false, "").Content, got) {
		t.Fatalf("message[0].content = %q, want prefix of default preamble", got)
	}
}

func TestPlanSourceAssemblyIncludesAndPlacesOptionalSources(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	projectRoot := t.TempDir()
	skillsRoot := t.TempDir()

	mustWrite(t, filepath.Join(homeDir, ".config", "steiner"), "AGENTS.md", "global rules")
	mustWrite(t, projectRoot, "AGENTS.md", "project rules")
	mustWrite(t, projectRoot, "README.md", "project readme")
	mustWrite(t, filepath.Join(skillsRoot, "codex"), "SKILL.md", "skill instructions")

	assembly := mustRenderPlannedAssembly(t, AssemblyOptions{
		HomeDir:     homeDir,
		ProjectRoot: projectRoot,
		SkillsRoots: []string{skillsRoot},
		SkillNames:  []string{"codex"},
		ContextState: DurableContextState{
			RetainedSummaries: []DurableSummaryEntry{
				{Title: "retained conversation", Text: "earlier request and tool output", Source: "loop_compaction", Turn: 2},
			},
		},
		Conversation: []provider.Message{
			{Role: provider.MessageRoleUser, Content: "conversation turn"},
		},
		ToolResults: []provider.Message{
			{Role: provider.MessageRoleTool, Content: "tool output"},
		},
		ProjectContextBudgetBytes: 1024,
		ProjectContextExtraFiles:  []string{"README.md"},
	})

	if got, want := blockSources(assembly.Blocks), []ContextSource{
		ContextSourcePreamble,
		ContextSourceGlobalAgentsMD,
		ContextSourceProjectAgentsMD,
		ContextSourceProjectContext,
		ContextSourceSkill,
		ContextSourceSkill,
		ContextSourceToolSummary,
	}; !sourcesEqual(got, want) {
		t.Fatalf("block sources = %v, want %v", got, want)
	}

	if got, want := messageIndexContaining(assembly.Messages, "conversation turn"), messageIndexContaining(assembly.Messages, "\"kind\":\"tool_summary\""); got < 0 || want < 0 || got >= want {
		t.Fatalf("conversation should appear before tool summary: conversation=%d tool_summary=%d", got, want)
	}

	if got := messageIndexContaining(assembly.Messages, "skill instructions"); got < 0 {
		t.Fatalf("skill message not found")
	}
	if got := messageIndexContaining(assembly.Messages, "project readme"); got < 0 {
		t.Fatalf("project context message not found")
	}
}

func TestPlanSourceAssemblyIsBudgetIndependent(t *testing.T) {
	t.Parallel()

	lowBudgetAssembler := assembler{
		opts: AssemblyOptions{
			Policy: AssemblyPolicy{
				Budgets: SourceBudgetModel{
					PreambleBytes:       1,
					GlobalAgentsBytes:   1,
					ProjectAgentsBytes:  1,
					ProjectContextBytes: 1,
					SkillBytes:          1,
					ToolResultBytes:     1,
					ToolSummaryBytes:    1,
				},
			},
		},
	}
	highBudgetAssembler := assembler{
		opts: AssemblyOptions{
			Policy: AssemblyPolicy{
				Budgets: SourceBudgetModel{
					PreambleBytes:       1024,
					GlobalAgentsBytes:   2048,
					ProjectAgentsBytes:  8192,
					ProjectContextBytes: 4096,
					SkillBytes:          2048,
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

	plan := assembler.planSourceAssembly()

	assembly, err := plan.render(context.Background(), assembler.policy, assembler.opts)
	if err != nil {
		t.Fatalf("plan.render() error = %v", err)
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

func mustRenderPlannedAssembly(t *testing.T, opts AssemblyOptions) Assembly {
	t.Helper()

	assembler, err := newAssembler(opts)
	if err != nil {
		t.Fatalf("newAssembler() error = %v", err)
	}

	plan := assembler.planSourceAssembly()
	assembly, err := plan.render(context.Background(), assembler.policy, assembler.opts)
	if err != nil {
		t.Fatalf("plan.render() error = %v", err)
	}
	return assembly
}

func blockSources(blocks []ContextBlock) []ContextSource {
	sources := make([]ContextSource, 0, len(blocks))
	for _, block := range blocks {
		sources = append(sources, block.Source)
	}
	return sources
}

func sourcesEqual(got, want []ContextSource) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func messageIndexContaining(messages []provider.Message, substr string) int {
	for i, message := range messages {
		if strings.Contains(message.Content, substr) {
			return i
		}
	}
	return -1
}
