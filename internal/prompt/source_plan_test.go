package prompt

import (
	"context"
	"path/filepath"
	"reflect"
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
		{Kind: plannedSourcePhasePrompt, Placement: plannedSourcePlacementCore, PassThrough: false},
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
	if got := assembly.Messages[0].Content; !strings.HasPrefix(SystemPreamble("", false, false, "").Content, got) {
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

// TestAssembleKeepsStaticSourcesBeforeDynamicSources pins the prompt cache
// invariant: Assemble must emit static sources (preamble, agents, project
// context, skills, phase prompt) ahead of dynamic ones (conversation, tool
// summaries), and must do so deterministically across calls.
func TestAssembleKeepsStaticSourcesBeforeDynamicSources(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	projectRoot := t.TempDir()
	skillsRoot := t.TempDir()

	mustWrite(t, filepath.Join(homeDir, ".config", "steiner"), "AGENTS.md", "global rules")
	mustWrite(t, projectRoot, "AGENTS.md", "project rules")
	mustWrite(t, projectRoot, "README.md", "project readme")
	mustWrite(t, filepath.Join(skillsRoot, "codex"), "SKILL.md", "skill instructions")

	assembler, err := newAssembler(AssemblyOptions{
		HomeDir:                   homeDir,
		ProjectRoot:               projectRoot,
		SkillsRoots:               []string{skillsRoot},
		SkillNames:                []string{"codex"},
		PhasePrompt:               "phase instructions",
		ProjectContextBudgetBytes: 1024,
		ProjectContextExtraFiles:  []string{"README.md"},
		Conversation:              []provider.Message{{Role: provider.MessageRoleUser, Content: "conversation turn"}},
		ToolResults:               []provider.Message{{Role: provider.MessageRoleTool, Content: "tool output"}},
	})
	if err != nil {
		t.Fatalf("newAssembler() error = %v", err)
	}

	got, err := assembler.Assemble(context.Background())
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	wantSources := []ContextSource{
		ContextSourcePreamble,
		ContextSourceGlobalAgentsMD,
		ContextSourceProjectAgentsMD,
		ContextSourceProjectContext,
		ContextSourceSkill,
		ContextSourceSkill,
		ContextSourcePhasePrompt,
		ContextSourceToolSummary,
	}
	if gotSources := blockSources(got.Blocks); !sourcesEqual(gotSources, wantSources) {
		t.Fatalf("block sources = %v, want %v", gotSources, wantSources)
	}

	phaseIdx := messageIndexContaining(got.Messages, "phase instructions")
	conversationIdx := messageIndexContaining(got.Messages, "conversation turn")
	toolSummaryIdx := messageIndexContaining(got.Messages, "\"kind\":\"tool_summary\"")
	if phaseIdx < 0 || conversationIdx < 0 || toolSummaryIdx < 0 {
		t.Fatalf("missing messages: phase=%d conversation=%d tool_summary=%d", phaseIdx, conversationIdx, toolSummaryIdx)
	}
	if phaseIdx >= conversationIdx || conversationIdx >= toolSummaryIdx {
		t.Fatalf("message order phase=%d conversation=%d tool_summary=%d, want static sources first", phaseIdx, conversationIdx, toolSummaryIdx)
	}

	again, err := assembler.Assemble(context.Background())
	if err != nil {
		t.Fatalf("second Assemble() error = %v", err)
	}
	if !reflect.DeepEqual(got.Messages, again.Messages) {
		t.Fatal("Assemble() is not deterministic across calls; prompt prefix must be byte-stable")
	}
}

func TestPlanSourceAssemblySkipAgentsAndProjectContext(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	projectRoot := t.TempDir()

	mustWrite(t, filepath.Join(homeDir, ".config", "steiner"), "AGENTS.md", "global rules")
	mustWrite(t, projectRoot, "AGENTS.md", "project rules")
	mustWrite(t, projectRoot, "README.md", "project readme")

	tests := []struct {
		name               string
		skipAgents         bool
		skipProjectContext bool
		wantAgents         bool
		wantProjectContext bool
	}{
		{name: "skip project context only", skipProjectContext: true, wantAgents: true},
		{name: "skip agents only", skipAgents: true, wantProjectContext: true},
		{name: "skip both", skipAgents: true, skipProjectContext: true},
		{name: "skip neither", wantAgents: true, wantProjectContext: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assembly := mustRenderPlannedAssembly(t, AssemblyOptions{
				HomeDir:                   homeDir,
				ProjectRoot:               projectRoot,
				ProjectContextBudgetBytes: 1024,
				ProjectContextExtraFiles:  []string{"README.md"},
				SkipAgents:                tt.skipAgents,
				SkipProjectContext:        tt.skipProjectContext,
			})

			if got := messageIndexContaining(assembly.Messages, "global rules") >= 0; got != tt.wantAgents {
				t.Fatalf("global agents delivered = %t, want %t", got, tt.wantAgents)
			}
			if got := messageIndexContaining(assembly.Messages, "project rules") >= 0; got != tt.wantAgents {
				t.Fatalf("project agents delivered = %t, want %t", got, tt.wantAgents)
			}
			if got := messageIndexContaining(assembly.Messages, "project readme") >= 0; got != tt.wantProjectContext {
				t.Fatalf("project context delivered = %t, want %t", got, tt.wantProjectContext)
			}
			for _, block := range assembly.Blocks {
				if block.Source == ContextSourceProjectContext && !tt.wantProjectContext {
					t.Fatalf("unexpected project context block: path=%q", block.Path)
				}
			}
		})
	}
}

func TestPlanSourceAssemblyDeliversLargeAgentsFilesWhole(t *testing.T) {
	t.Parallel()

	large := strings.Repeat("x", 20000)

	t.Run("project agents", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		mustWrite(t, dir, "AGENTS.md", large)

		assembly := mustRenderPlannedAssembly(t, AssemblyOptions{
			ProjectAgentsPath: filepath.Join(dir, "AGENTS.md"),
		})

		var block *ContextBlock
		for i := range assembly.Blocks {
			if assembly.Blocks[i].Source == ContextSourceProjectAgentsMD {
				block = &assembly.Blocks[i]
				break
			}
		}
		if block == nil {
			t.Fatal("project agents block not found")
		}
		if got, want := block.ByteSize, len(large); got != want {
			t.Fatalf("project agents block bytes = %d, want %d", got, want)
		}
		if block.Truncated {
			t.Fatal("project agents block unexpectedly truncated")
		}
		if got := block.Content; got != large {
			t.Fatalf("project agents content mismatch: got %d bytes, want %d bytes", len(got), len(large))
		}
	})

	t.Run("global agents", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		mustWrite(t, dir, "AGENTS.md", large)

		assembly := mustRenderPlannedAssembly(t, AssemblyOptions{
			GlobalAgentsPath: filepath.Join(dir, "AGENTS.md"),
		})

		var block *ContextBlock
		for i := range assembly.Blocks {
			if assembly.Blocks[i].Source == ContextSourceGlobalAgentsMD {
				block = &assembly.Blocks[i]
				break
			}
		}
		if block == nil {
			t.Fatal("global agents block not found")
		}
		if got, want := block.ByteSize, len(large); got != want {
			t.Fatalf("global agents block bytes = %d, want %d", got, want)
		}
		if block.Truncated {
			t.Fatal("global agents block unexpectedly truncated")
		}
		if got := block.Content; got != large {
			t.Fatalf("global agents content mismatch: got %d bytes, want %d bytes", len(got), len(large))
		}
	})
}

// TestPlanSourceAssemblyMergesAgentsIntoPreamble guards the merge invariant:
// both AGENTS.md blocks must route through renderBlocks so they fold into the
// preamble's single system message, and conversation messages follow in order
// with their roles preserved.
func TestPlanSourceAssemblyMergesAgentsIntoPreamble(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	projectRoot := t.TempDir()
	mustWrite(t, filepath.Join(homeDir, ".config", "steiner"), "AGENTS.md", "global agents content")
	mustWrite(t, projectRoot, "AGENTS.md", "project agents content")

	assembly := mustRenderPlannedAssembly(t, AssemblyOptions{
		HomeDir:     homeDir,
		ProjectRoot: projectRoot,
		Conversation: []provider.Message{
			{Role: provider.MessageRoleUser, Content: "user turn one"},
			{Role: provider.MessageRoleAssistant, Content: "assistant turn one"},
		},
	})

	systemCount := 0
	var systemContent string
	for _, m := range assembly.Messages {
		if m.Role == provider.MessageRoleSystem {
			systemCount++
			systemContent = m.Content
		}
	}
	if systemCount != 1 {
		t.Fatalf("system message count = %d, want exactly 1", systemCount)
	}
	if preamble := SystemPreamble("", false, false, "").Content; !strings.Contains(systemContent, preamble) {
		t.Fatalf("system message missing preamble text")
	}
	if !strings.Contains(systemContent, "global agents content") {
		t.Fatalf("system message missing global agents content: %q", systemContent)
	}
	if !strings.Contains(systemContent, "project agents content") {
		t.Fatalf("system message missing project agents content: %q", systemContent)
	}

	if got, want := len(assembly.Messages), 3; got != want {
		t.Fatalf("len(messages) = %d, want %d", got, want)
	}
	wantMessages := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "user turn one"},
		{Role: provider.MessageRoleAssistant, Content: "assistant turn one"},
	}
	for i, want := range wantMessages {
		got := assembly.Messages[i+1]
		if got.Role != want.Role || got.Content != want.Content {
			t.Fatalf("message[%d] = {role:%q content:%q}, want {role:%q content:%q}", i+1, got.Role, got.Content, want.Role, want.Content)
		}
	}
}

func TestPlanSourceAssemblyProjectAgentsPathOverridesProjectRoot(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	mustWrite(t, projectRoot, "AGENTS.md", "content A")

	overrideDir := t.TempDir()
	mustWrite(t, overrideDir, "AGENTS.md", "content B")

	assembly := mustRenderPlannedAssembly(t, AssemblyOptions{
		ProjectRoot:       projectRoot,
		ProjectAgentsPath: filepath.Join(overrideDir, "AGENTS.md"),
	})

	count := 0
	for _, block := range assembly.Blocks {
		if block.Source != ContextSourceProjectAgentsMD {
			continue
		}
		count++
		if block.Content != "content B" {
			t.Fatalf("project agents content = %q, want %q", block.Content, "content B")
		}
	}
	if count != 1 {
		t.Fatalf("project agents block count = %d, want 1", count)
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
