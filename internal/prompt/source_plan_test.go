package prompt

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
		{Kind: plannedSourceSessionDate, Placement: plannedSourcePlacementCore, PassThrough: false},
		{Kind: plannedSourceConversation, Placement: plannedSourcePlacementConversation, PassThrough: true},
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
	}; !sourcesEqual(got, want) {
		t.Fatalf("block sources = %v, want %v", got, want)
	}

	if got := messageIndexContaining(assembly.Messages, "conversation turn"); got < 0 {
		t.Fatalf("conversation message not found")
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
					ProjectContextBytes: 1,
					SkillBytes:          1,
				},
			},
		},
	}
	highBudgetAssembler := assembler{
		opts: AssemblyOptions{
			Policy: AssemblyPolicy{
				Budgets: SourceBudgetModel{
					ProjectContextBytes: 4096,
					SkillBytes:          2048,
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
// context, skills, phase prompt, session date) ahead of dynamic ones (conversation), and
// must do so deterministically across calls.
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
	}
	if gotSources := blockSources(got.Blocks); !sourcesEqual(gotSources, wantSources) {
		t.Fatalf("block sources = %v, want %v", gotSources, wantSources)
	}

	phaseIdx := messageIndexContaining(got.Messages, "phase instructions")
	conversationIdx := messageIndexContaining(got.Messages, "conversation turn")
	if phaseIdx < 0 || conversationIdx < 0 {
		t.Fatalf("missing messages: phase=%d conversation=%d", phaseIdx, conversationIdx)
	}
	if phaseIdx >= conversationIdx {
		t.Fatalf("message order phase=%d conversation=%d, want static sources first", phaseIdx, conversationIdx)
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

func TestSessionDateIncludedBeforeConversation(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	skillsRoot := t.TempDir()
	mustWrite(t, filepath.Join(skillsRoot, "test"), "SKILL.md", "skill content")

	sessionDate := NewSessionDate(time.Date(2026, 9, 13, 12, 30, 0, 0, time.FixedZone("BST", 3600)))

	assembly := mustRenderPlannedAssembly(t, AssemblyOptions{
		HomeDir:     homeDir,
		SkillsRoots: []string{skillsRoot},
		SkillNames:  []string{"test"},
		SessionDate: sessionDate,
		Conversation: []provider.Message{
			{Role: provider.MessageRoleUser, Content: "conversation turn"},
		},
	})

	var dateBlock *ContextBlock
	for i := range assembly.Blocks {
		if assembly.Blocks[i].Source == ContextSourceSessionDate {
			dateBlock = &assembly.Blocks[i]
			break
		}
	}
	if dateBlock == nil {
		t.Fatal("session date block not found")
	}
	if want := "Current date: 2026-09-13 (BST, UTC+01:00), recorded when this session started."; dateBlock.Content != want {
		t.Fatalf("session date content = %q, want %q", dateBlock.Content, want)
	}

	conversationIdx := messageIndexContaining(assembly.Messages, "conversation turn")
	if conversationIdx < 0 {
		t.Fatal("conversation message not found")
	}
	if conversationIdx < 1 {
		t.Fatalf("conversation message at position %d, want position >= 1", conversationIdx)
	}
}

func TestSessionDateWithPhasePromptCreatesOwnMessage(t *testing.T) {
	t.Parallel()

	sessionDate := NewSessionDate(time.Date(2026, 9, 13, 12, 30, 0, 0, time.FixedZone("BST", 3600)))

	assembly := mustRenderPlannedAssembly(t, AssemblyOptions{
		PhasePrompt: "phase instructions",
		SessionDate: sessionDate,
		Conversation: []provider.Message{
			{Role: provider.MessageRoleUser, Content: "conversation turn"},
		},
	})

	blocks := blockSources(assembly.Blocks)
	wantBlocks := []ContextSource{
		ContextSourcePreamble,
		ContextSourcePhasePrompt,
		ContextSourceSessionDate,
	}
	if !sourcesEqual(blocks, wantBlocks) {
		t.Fatalf("block sources = %v, want %v", blocks, wantBlocks)
	}

	phaseIdx := messageIndexContaining(assembly.Messages, "phase instructions")
	dateIdx := messageIndexContaining(assembly.Messages, "Current date")
	conversationIdx := messageIndexContaining(assembly.Messages, "conversation turn")

	if phaseIdx < 0 || dateIdx < 0 || conversationIdx < 0 {
		t.Fatalf("missing messages: phase=%d date=%d conversation=%d", phaseIdx, dateIdx, conversationIdx)
	}
	if phaseIdx >= dateIdx || dateIdx >= conversationIdx {
		t.Fatalf("message order phase=%d date=%d conversation=%d, want phase < date < conversation", phaseIdx, dateIdx, conversationIdx)
	}
}

func TestSessionDateZeroValueOmitsBlock(t *testing.T) {
	t.Parallel()

	optionsWithoutDate := AssemblyOptions{
		Conversation: []provider.Message{
			{Role: provider.MessageRoleUser, Content: "user message"},
		},
	}

	optionsWithZeroDate := AssemblyOptions{
		SessionDate: SessionDate{},
		Conversation: []provider.Message{
			{Role: provider.MessageRoleUser, Content: "user message"},
		},
	}

	assemblyWithout, err := newAssembler(optionsWithoutDate)
	if err != nil {
		t.Fatalf("newAssembler(without) error = %v", err)
	}
	resultWithout, err := assemblyWithout.Assemble(context.Background())
	if err != nil {
		t.Fatalf("Assemble(without) error = %v", err)
	}

	assemblyWithZero, err := newAssembler(optionsWithZeroDate)
	if err != nil {
		t.Fatalf("newAssembler(withZero) error = %v", err)
	}
	resultWithZero, err := assemblyWithZero.Assemble(context.Background())
	if err != nil {
		t.Fatalf("Assemble(withZero) error = %v", err)
	}

	if !reflect.DeepEqual(resultWithout.Messages, resultWithZero.Messages) {
		t.Fatal("Assemble() differs for zero SessionDate: zero-value must be omitted")
	}
	if !reflect.DeepEqual(resultWithout.Blocks, resultWithZero.Blocks) {
		t.Fatal("Assemble() blocks differ for zero SessionDate: zero-value must be omitted")
	}
}

func TestSessionDateBypassesBudget(t *testing.T) {
	t.Parallel()

	skillsRoot := t.TempDir()
	largeSkillContent := strings.Repeat("x", 2000)
	mustWrite(t, filepath.Join(skillsRoot, "large"), "SKILL.md", largeSkillContent)

	sessionDate := NewSessionDate(time.Date(2026, 9, 13, 12, 30, 0, 0, time.UTC))

	assembly := mustRenderPlannedAssembly(t, AssemblyOptions{
		SkillsRoots: []string{skillsRoot},
		SkillNames:  []string{"large"},
		SessionDate: sessionDate,
		Policy: AssemblyPolicy{
			Budgets: SourceBudgetModel{
				SkillBytes: 1,
			},
		},
	})

	var dateBlock *ContextBlock
	var skillBlock *ContextBlock
	for i := range assembly.Blocks {
		if assembly.Blocks[i].Source == ContextSourceSessionDate {
			dateBlock = &assembly.Blocks[i]
		}
		if assembly.Blocks[i].Source == ContextSourceSkill {
			skillBlock = &assembly.Blocks[i]
		}
	}

	if dateBlock == nil {
		t.Fatal("session date block not found")
	}
	wantDateContent := "Current date: 2026-09-13 (UTC, UTC+00:00), recorded when this session started."
	if dateBlock.Content != wantDateContent {
		t.Fatalf("session date content = %q, want %q", dateBlock.Content, wantDateContent)
	}
	if dateBlock.Truncated {
		t.Fatal("session date block unexpectedly truncated despite bypass budget")
	}

	if skillBlock == nil {
		t.Fatal("skill block not found")
	}
	if !skillBlock.Truncated || skillBlock.ByteSize >= len(largeSkillContent) {
		t.Fatalf("skill block not truncated as expected; content_size=%d full_size=%d truncated=%t", skillBlock.ByteSize, len(largeSkillContent), skillBlock.Truncated)
	}
}

func TestSessionDateStabilityAcrossConversationLengths(t *testing.T) {
	t.Parallel()

	skillsRoot := t.TempDir()
	mustWrite(t, filepath.Join(skillsRoot, "test"), "SKILL.md", "skill content")

	sessionDate := NewSessionDate(time.Date(2026, 9, 13, 12, 30, 0, 0, time.UTC))

	baseOpts := AssemblyOptions{
		SkillsRoots: []string{skillsRoot},
		SkillNames:  []string{"test"},
		SessionDate: sessionDate,
	}

	opts1 := baseOpts
	opts1.Conversation = []provider.Message{
		{Role: provider.MessageRoleUser, Content: "short message"},
	}

	opts2 := baseOpts
	opts2.Conversation = []provider.Message{
		{Role: provider.MessageRoleUser, Content: "short message"},
		{Role: provider.MessageRoleAssistant, Content: strings.Repeat("long response content ", 50)},
		{Role: provider.MessageRoleUser, Content: "follow-up question"},
	}

	assembly1, err := newAssembler(opts1)
	if err != nil {
		t.Fatalf("newAssembler(1) error = %v", err)
	}
	result1, err := assembly1.Assemble(context.Background())
	if err != nil {
		t.Fatalf("Assemble(1) error = %v", err)
	}

	assembly2, err := newAssembler(opts2)
	if err != nil {
		t.Fatalf("newAssembler(2) error = %v", err)
	}
	result2, err := assembly2.Assemble(context.Background())
	if err != nil {
		t.Fatalf("Assemble(2) error = %v", err)
	}

	conversationStartIdx1 := messageIndexContaining(result1.Messages, "short message")
	conversationStartIdx2 := messageIndexContaining(result2.Messages, "short message")

	if conversationStartIdx1 < 0 || conversationStartIdx2 < 0 {
		t.Fatalf("conversation not found: idx1=%d idx2=%d", conversationStartIdx1, conversationStartIdx2)
	}

	for i := 0; i < conversationStartIdx1; i++ {
		if result1.Messages[i].Content != result2.Messages[i].Content {
			t.Fatalf("static prefix differs at message %d: %q vs %q", i, result1.Messages[i].Content, result2.Messages[i].Content)
		}
		if result1.Messages[i].Role != result2.Messages[i].Role {
			t.Fatalf("static prefix role differs at message %d: %q vs %q", i, result1.Messages[i].Role, result2.Messages[i].Role)
		}
	}
}

func mustRenderPlannedAssembly(t *testing.T, opts AssemblyOptions) Assembly {
	t.Helper()

	assembler, err := newAssembler(opts)
	if err != nil {
		t.Fatalf("newAssembler() error = %v", err)
	}

	assembly, err := assembler.Assemble(context.Background())
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
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
