package prompt

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

//nolint:gocyclo
func TestAssembleOrdersContextAndSkipsImplicitSkills(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	projectRoot := t.TempDir()
	skillsRoot := t.TempDir()

	mustWrite(t, filepath.Join(homeDir, ".config", "steiner"), "AGENTS.md", "global rules")
	mustWrite(t, projectRoot, "AGENTS.md", "project rules")
	mustWrite(t, projectRoot, "README.md", "project readme")
	mustWrite(t, projectRoot, "go.mod", "module example.com/test\n")
	mustWrite(t, filepath.Join(skillsRoot, "codex"), "SKILL.md", "skill instructions")

	assembly, err := Assemble(context.Background(), AssemblyOptions{
		HomeDir:                   homeDir,
		ProjectRoot:               projectRoot,
		SkillsRoots:               []string{skillsRoot},
		ContextState:              DurableContextState{RetainedSummaries: []DurableSummaryEntry{{Title: "summary", Text: "retained compaction summary", Source: "compactor", Turn: 4}}},
		Conversation:              []provider.Message{{Role: provider.MessageRoleUser, Content: "how do I fix this?"}, {Role: provider.MessageRoleAssistant, Content: "use the tools"}},
		ToolResults:               []provider.Message{{Role: provider.MessageRoleTool, Content: "tool result"}},
		ProjectContextBudgetBytes: 1024,
		ProjectContextExtraFiles:  []string{"README.md", "go.mod"},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	if got, want := len(assembly.Blocks), 6; got != want {
		t.Fatalf("len(blocks) = %d, want %d", got, want)
	}

	wantSources := []ContextSource{
		ContextSourcePreamble,
		ContextSourceGlobalAgentsMD,
		ContextSourceProjectAgentsMD,
		ContextSourceProjectContext,
		ContextSourceProjectContext,
		ContextSourceToolSummary,
	}
	for i, want := range wantSources {
		if got := assembly.Blocks[i].Source; got != want {
			t.Fatalf("block %d source = %q, want %q", i, got, want)
		}
	}

	if got, want := len(assembly.Messages), 5; got != want {
		t.Fatalf("len(messages) = %d, want %d", got, want)
	}

	if got := assembly.Messages[0].Role; got != provider.MessageRoleSystem {
		t.Fatalf("message[0].role = %q, want system", got)
	}

	sysMsg := assembly.Messages[0].Content
	if !strings.Contains(sysMsg, "global rules") {
		t.Fatalf("system message missing global rules: %q", sysMsg)
	}
	if !strings.Contains(sysMsg, "project rules") {
		t.Fatalf("system message missing project rules: %q", sysMsg)
	}

	readme := messageIndexByNameContains(t, assembly.Messages, "README.md")
	conversation := messageIndexByContent(t, assembly.Messages, "how do I fix this?")
	toolSummary := messageIndexContaining(assembly.Messages, "\"kind\":\"tool_summary\"")

	if readme <= 0 || readme >= conversation || conversation >= toolSummary {
		t.Fatalf("message order = readme:%d conversation:%d tool_summary:%d", readme, conversation, toolSummary)
	}
	if got := strings.Contains(assembly.Messages[0].Content, "retained compaction summary"); got {
		t.Fatalf("system message unexpectedly includes retained compaction summary: %q", assembly.Messages[0].Content)
	}
}

func TestAssembleLoadsExplicitSkills(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	projectRoot := t.TempDir()
	skillsRoot := t.TempDir()

	mustWrite(t, filepath.Join(skillsRoot, "codex"), "SKILL.md", "skill instructions")

	assembly, err := Assemble(context.Background(), AssemblyOptions{
		HomeDir:                   homeDir,
		ProjectRoot:               projectRoot,
		SkillsRoots:               []string{skillsRoot},
		SkillNames:                []string{"codex"},
		ProjectContextBudgetBytes: 1,
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	foundSkillBlocks := 0
	var contentBlock ContextBlock
	for _, block := range assembly.Blocks {
		if block.Source == ContextSourceSkill {
			foundSkillBlocks++
			if strings.Contains(block.Content, "skill instructions") {
				contentBlock = block
			}
		}
	}
	if got, want := foundSkillBlocks, 2; got != want {
		t.Fatalf("found %d skill blocks, want %d", got, want)
	}
	if !strings.Contains(contentBlock.Content, "skill instructions") {
		t.Fatalf("skill content block not found")
	}

	gotIndex := messageIndexContaining(assembly.Messages, "skill instructions")
	if gotIndex < 0 {
		t.Fatalf("skill message not found")
	}
	if got := assembly.Messages[gotIndex].Role; got != provider.MessageRoleUser {
		t.Fatalf("skill message role = %q, want user", got)
	}
	if got := assembly.Messages[gotIndex].Content; !strings.Contains(got, "## Active Skills") {
		t.Fatalf("skill message content missing framing block: %q", got)
	}
}

func TestGatherProjectContextHonorsBudget(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	mustWrite(t, projectRoot, "README.md", "1234567890")
	mustWrite(t, projectRoot, "go.mod", "module example.com/test\n")

	blocks, err := gatherProjectContext(ProjectContextOptions{
		Root:        projectRoot,
		BudgetBytes: 5,
		ExtraFiles:  []string{"README.md", "go.mod"},
	})
	if err != nil {
		t.Fatalf("gatherProjectContext() error = %v", err)
	}

	if got, want := len(blocks), 1; got != want {
		t.Fatalf("len(blocks) = %d, want %d", got, want)
	}
	if got, want := blocks[0].Content, "12345"; got != want {
		t.Fatalf("block content = %q, want %q", got, want)
	}
	if !blocks[0].Truncated {
		t.Fatalf("expected block to be truncated")
	}
	if got, want := blocks[0].ByteSize, 5; got != want {
		t.Fatalf("block bytes = %d, want %d", got, want)
	}
}

//nolint:gocyclo
func TestAssembleClipsRenderedBlocksByBudget(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	mustWrite(t, projectRoot, "README.md", "project context content")

	assembly, err := Assemble(context.Background(), AssemblyOptions{
		ProjectRoot:              projectRoot,
		ProjectContextExtraFiles: []string{"README.md"},
		Policy: AssemblyPolicy{
			Budgets: SourceBudgetModel{
				PreambleBytes:       5,
				ProjectContextBytes: 4,
			},
		},
		PromptOverrides: config.ModelPrompts{
			System: "system prompt content",
		},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	if got, want := len(assembly.Blocks), 2; got != want {
		t.Fatalf("len(blocks) = %d, want %d", got, want)
	}

	preamble := assembly.Blocks[0].Content
	if !strings.HasPrefix(preamble, testIdentityMarker) {
		t.Fatalf("preamble block missing identity marker in %q", preamble)
	}
	if !strings.HasSuffix(strings.TrimSpace(preamble), "system prompt content") {
		t.Fatalf("preamble block missing override suffix in %q", preamble)
	}
	if assembly.Blocks[0].Truncated {
		t.Fatalf("expected preamble block not to be truncated (bypasses budget)")
	}
	if got, want := assembly.Blocks[0].ByteSize, len(preamble); got != want {
		t.Fatalf("preamble block bytes = %d, want %d", got, want)
	}

	if got, want := assembly.Blocks[1].Content, "proj"; got != want {
		t.Fatalf("project context block content = %q, want %q", got, want)
	}
	if !assembly.Blocks[1].Truncated {
		t.Fatalf("expected project context block to be truncated")
	}
	if got, want := assembly.Blocks[1].ByteSize, 4; got != want {
		t.Fatalf("project context block bytes = %d, want %d", got, want)
	}

	if got, want := len(assembly.Messages), 2; got != want {
		t.Fatalf("len(messages) = %d, want %d", got, want)
	}
	messagePreamble := assembly.Messages[0].Content
	if !strings.HasPrefix(messagePreamble, testIdentityMarker) {
		t.Fatalf("preamble message missing identity marker in %q", messagePreamble)
	}
	if !strings.HasSuffix(strings.TrimSpace(messagePreamble), "system prompt content") {
		t.Fatalf("preamble message missing override suffix in %q", messagePreamble)
	}
	if got, want := assembly.Messages[1].Content, "proj"; got != want {
		t.Fatalf("project context message content = %q, want %q", got, want)
	}
	if got, want := assembly.Messages[1].Role, provider.MessageRoleUser; got != want {
		t.Fatalf("project context message role = %q, want %q", got, want)
	}
}

func TestAssembleMergesConsecutiveSystemMessages(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	projectRoot := t.TempDir()

	mustWrite(t, filepath.Join(homeDir, ".config", "steiner"), "AGENTS.md", "global agents")
	mustWrite(t, projectRoot, "AGENTS.md", "project agents")

	assembly, err := Assemble(context.Background(), AssemblyOptions{
		HomeDir:     homeDir,
		ProjectRoot: projectRoot,
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	if got, want := len(assembly.Messages), 1; got != want {
		t.Fatalf("len(messages) = %d, want %d", got, want)
	}

	msg := assembly.Messages[0]
	if got, want := msg.Role, provider.MessageRoleSystem; got != want {
		t.Fatalf("message role = %q, want %q", got, want)
	}

	parts := strings.Split(msg.Content, "\n\n")
	if len(parts) < 2 {
		t.Fatalf("expected at least 2 system parts separated by \\n\\n, got %d parts: %q", len(parts), msg.Content)
	}

	foundGlobal := false
	foundProject := false
	for _, part := range parts {
		if strings.Contains(part, "global agents") {
			foundGlobal = true
		}
		if strings.Contains(part, "project agents") {
			foundProject = true
		}
	}
	if !foundGlobal {
		t.Fatalf("merged system message missing global agents content: %q", msg.Content)
	}
	if !foundProject {
		t.Fatalf("merged system message missing project agents content: %q", msg.Content)
	}
}

func TestGatherProjectContextDoesNotLoadImplicitFilesByDefault(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	mustWrite(t, projectRoot, "README.md", "project readme")
	mustWrite(t, projectRoot, "go.mod", "module example.com/test\n")

	blocks, err := gatherProjectContext(ProjectContextOptions{
		Root:        projectRoot,
		BudgetBytes: 1024,
	})
	if err != nil {
		t.Fatalf("gatherProjectContext() error = %v", err)
	}
	if len(blocks) != 0 {
		t.Fatalf("len(blocks) = %d, want 0 with no explicit extra files", len(blocks))
	}
}

// TestAssemblePassesFullConversationUnfiltered verifies that all conversation
// messages are forwarded to the model without byte-budget filtering, even when
// the total size exceeds the old 32 KB limit, and that in-progress tool
// exchanges (assistant tool_call + tool result) are never dropped.
func TestAssemblePassesFullConversationUnfiltered(t *testing.T) {
	t.Parallel()

	// Build a conversation whose total size exceeds 32768 bytes (old limit).
	bigContent := strings.Repeat("a", 4096)
	conversation := make([]provider.Message, 0, 20)
	for i := range 8 {
		conversation = append(conversation,
			provider.Message{Role: provider.MessageRoleUser, Content: fmt.Sprintf("user turn %d: %s", i, bigContent)},
			provider.Message{Role: provider.MessageRoleAssistant, Content: fmt.Sprintf("assistant turn %d: %s", i, bigContent)},
		)
	}

	// Append an in-progress tool exchange at the end.
	toolCallMsg := provider.Message{Role: provider.MessageRoleAssistant, Content: `{"tool":"read","id":"call_1"}`}
	toolResultMsg := provider.Message{Role: provider.MessageRoleTool, Name: "read", Content: "file contents"}
	conversation = append(conversation, toolCallMsg, toolResultMsg)

	assembly, err := Assemble(context.Background(), AssemblyOptions{
		Conversation: conversation,
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	// Every conversation message must appear verbatim in Assembly.Messages.
	msgMap := make(map[string]bool, len(assembly.Messages))
	for _, m := range assembly.Messages {
		msgMap[m.Content] = true
	}

	if !msgMap[toolCallMsg.Content] {
		t.Errorf("assistant tool_call message missing from Assembly.Messages")
	}
	if !msgMap[toolResultMsg.Content] {
		t.Errorf("tool result message missing from Assembly.Messages")
	}

	// Count conversation messages in the output (preamble system message comes first).
	convCount := 0
	for _, m := range assembly.Messages {
		for _, cm := range conversation {
			if m.Content == cm.Content && m.Role == cm.Role {
				convCount++
				break
			}
		}
	}
	if convCount != len(conversation) {
		t.Errorf("Assembly.Messages contains %d/%d conversation messages; want all", convCount, len(conversation))
	}
}

func TestAssembleRetainedSummariesAreNotInjectedIntoSystemPrompt(t *testing.T) {
	t.Parallel()

	assembly, err := Assemble(context.Background(), AssemblyOptions{
		Policy: AssemblyPolicy{
			Compaction: CompactionPolicy{SummaryBytes: 512},
		},
		ContextState: DurableContextState{
			RetainedSummaries: []DurableSummaryEntry{
				{Title: "compacted conversation history", Text: "earlier request and tool output", Source: "loop_compaction", Turn: 2},
			},
		},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	// Retained summaries should no longer produce a durable_context block in
	// the system prompt zone — they are injected via the volatile zone instead.
	for _, block := range assembly.Blocks {
		if block.Source == ContextSourceDurableContext {
			t.Fatalf("unexpected durable_context block in system prompt: %+v", block)
		}
	}
}

func TestRenderConversationCompactionInstructionNormalAndEmergency(t *testing.T) {
	t.Parallel()

	normal := RenderConversationCompactionInstruction("", CompactionModeNormal, false)
	if !strings.Contains(normal, "You are compacting the current working context for a coding agent.") {
		t.Fatalf("normal compaction instruction = %q, want standard instruction body", normal)
	}
	if strings.Contains(normal, "emergency handoff") {
		t.Fatalf("normal compaction instruction = %q, want no emergency guidance", normal)
	}

	emergency := RenderConversationCompactionInstruction("", CompactionModeEmergency, false)
	if !strings.Contains(emergency, "You are compacting the current working context for a coding agent.") {
		t.Fatalf("emergency compaction instruction = %q, want standard instruction body", emergency)
	}
	if !strings.Contains(emergency, "emergency handoff") {
		t.Fatalf("emergency compaction instruction = %q, want emergency guidance", emergency)
	}
}

func TestRenderConversationCompactionInstructionPreservesOverrideAndCaveHuman(t *testing.T) {
	t.Parallel()

	override := RenderConversationCompactionInstruction("custom compaction prompt", CompactionModeNormal, true)
	if got, want := override, "custom compaction prompt"; got != want {
		t.Fatalf("override compaction instruction = %q, want %q", got, want)
	}

	caveHuman := RenderConversationCompactionInstruction("", CompactionModeNormal, true)
	if !strings.Contains(caveHuman, "compact working context for coding agent") {
		t.Fatalf("cave-human compaction instruction = %q, want cave-human body", caveHuman)
	}
	if !strings.Contains(caveHuman, "Encoding directives:") {
		t.Fatalf("cave-human compaction instruction = %q, want encoding directives block", caveHuman)
	}
}

func TestRenderConversationCompactionInstructionSteering(t *testing.T) {
	t.Parallel()
	const steering = "focus on auth refactor"
	const marker = "Additional user steering for this compaction:\n\n" + steering

	tests := []struct {
		name     string
		override string
		mode     CompactionMode
		wantBase string
		wantTail string
	}{
		{name: "default", wantBase: "You are compacting the current working context for a coding agent."},
		{name: "override", override: "custom compaction prompt", wantBase: "custom compaction prompt"},
		{name: "emergency", mode: CompactionModeEmergency, wantBase: "You are compacting the current working context for a coding agent.", wantTail: "emergency handoff"},
		{name: "override and emergency", override: "custom compaction prompt", mode: CompactionModeEmergency, wantBase: "custom compaction prompt", wantTail: "emergency handoff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderConversationCompactionInstruction(tt.override, tt.mode, false, steering)
			if !strings.Contains(got, tt.wantBase) || !strings.Contains(got, marker) {
				t.Fatalf("instruction = %q, want base %q and steering", got, tt.wantBase)
			}
			if tt.wantTail != "" && !strings.Contains(got, tt.wantTail) {
				t.Fatalf("instruction = %q, want emergency suffix", got)
			}
			if strings.Index(got, marker) < strings.Index(got, tt.wantBase) {
				t.Fatalf("steering appears before base instruction: %q", got)
			}
			if tt.wantTail != "" && strings.Index(got, marker) > strings.Index(got, tt.wantTail) {
				t.Fatalf("steering appears after emergency suffix: %q", got)
			}
		})
	}
	bare := RenderConversationCompactionInstruction("", CompactionModeNormal, false)
	empty := RenderConversationCompactionInstruction("", CompactionModeNormal, false, "")
	if bare != empty {
		t.Fatalf("empty steering changed output: bare=%q empty=%q", bare, empty)
	}
}

// makeBundledFS builds a test fs.FS with the given skill name and content.
func makeBundledFS(t *testing.T, content string) fs.FS {
	t.Helper()
	return fstest.MapFS{
		"codex/SKILL.md": &fstest.MapFile{Data: []byte(content)},
	}
}

func TestAssembleLoadsBundledSkills(t *testing.T) {
	t.Parallel()

	bfs := makeBundledFS(t, "bundled skill instructions")

	assembly, err := Assemble(context.Background(), AssemblyOptions{
		SkillsBundledFS: bfs,
		SkillNames:      []string{"codex"},
		Conversation:    []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	foundSkillBlocks := 0
	var contentBlock ContextBlock
	for _, block := range assembly.Blocks {
		if block.Source == ContextSourceSkill {
			foundSkillBlocks++
			if strings.Contains(block.Content, "bundled skill instructions") {
				contentBlock = block
			}
		}
	}
	if got, want := foundSkillBlocks, 2; got != want {
		t.Fatalf("found %d skill blocks, want %d", got, want)
	}
	if !strings.Contains(contentBlock.Content, "bundled skill instructions") {
		t.Fatalf("skill content block not found")
	}

	gotIndex := messageIndexContaining(assembly.Messages, "bundled skill instructions")
	if gotIndex < 0 {
		t.Fatalf("skill message not found")
	}
	if got := assembly.Messages[gotIndex].Role; got != provider.MessageRoleUser {
		t.Fatalf("skill message role = %q, want user", got)
	}
	if got := assembly.Messages[gotIndex].Content; !strings.Contains(got, "## Active Skills") {
		t.Fatalf("skill message content missing framing block: %q", got)
	}
}

func TestAssembleBundledSkillWithoutFilesystemRoots(t *testing.T) {
	t.Parallel()

	bfs := makeBundledFS(t, "bundled-only skill")

	assembly, err := Assemble(context.Background(), AssemblyOptions{
		HomeDir:         t.TempDir(),
		ProjectRoot:     t.TempDir(),
		SkillsBundledFS: bfs,
		SkillsRoots:     nil,
		SkillNames:      []string{"codex"},
		Conversation:    []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	gotIndex := messageIndexContaining(assembly.Messages, "bundled-only skill")
	if gotIndex < 0 {
		t.Fatalf("skill message not found")
	}
	if got := assembly.Messages[gotIndex].Role; got != provider.MessageRoleUser {
		t.Fatalf("skill message role = %q, want user", got)
	}
}

func TestAssembleBundledSkillPrecedenceOverFilesystem(t *testing.T) {
	t.Parallel()

	skillsRoot := t.TempDir()
	mustWrite(t, filepath.Join(skillsRoot, "codex"), "SKILL.md", "filesystem version")

	bfs := makeBundledFS(t, "bundled version")

	assembly, err := Assemble(context.Background(), AssemblyOptions{
		SkillsBundledFS: bfs,
		SkillsRoots:     []string{skillsRoot},
		SkillNames:      []string{"codex"},
		Conversation:    []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	gotIndex := messageIndexContaining(assembly.Messages, "bundled version")
	if gotIndex < 0 {
		t.Fatalf("skill message with bundled content not found")
	}

	if idx := messageIndexContaining(assembly.Messages, "filesystem version"); idx >= 0 {
		t.Fatalf("found filesystem version at message %d, expected bundled to shadow it", idx)
	}
}

func TestAssembleBundledSkillsAreNotImplicit(t *testing.T) {
	t.Parallel()

	bfs := makeBundledFS(t, "should not appear")

	assembly, err := Assemble(context.Background(), AssemblyOptions{
		SkillsBundledFS: bfs,
		Conversation:    []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	for _, block := range assembly.Blocks {
		if block.Source == ContextSourceSkill {
			t.Fatalf("unexpected skill block when SkillNames is empty: %q", block.Content)
		}
	}
	if idx := messageIndexContaining(assembly.Messages, "should not appear"); idx >= 0 {
		t.Fatalf("skill content found at message %d when SkillNames was empty", idx)
	}
}

func TestPhasePromptBypassesBudget(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	projectRoot := t.TempDir()

	// Create a large phase prompt. Note: phasePromptStep trims whitespace, so
	// the trailing space will be removed.
	phasePrompt := strings.Repeat("Phase prompt content.", 1000) + "\n"
	expectedPrompt := strings.TrimSpace(phasePrompt)

	// Set a very restrictive budget that would normally truncate the phase prompt
	assembly, err := Assemble(context.Background(), AssemblyOptions{
		HomeDir:      homeDir,
		ProjectRoot:  projectRoot,
		PhasePrompt:  phasePrompt,
		Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "hello"}},
		Policy: AssemblyPolicy{
			Budgets: SourceBudgetModel{
				PreambleBytes:       100000,
				ProjectContextBytes: 100,
				SkillBytes:          100,
			},
		},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	var foundPhasePrompt *ContextBlock
	for i := range assembly.Blocks {
		if assembly.Blocks[i].Source == ContextSourcePhasePrompt {
			foundPhasePrompt = &assembly.Blocks[i]
			break
		}
	}

	if foundPhasePrompt == nil {
		t.Fatal("phase prompt block not found in assembly")
	}

	if foundPhasePrompt.Truncated {
		t.Errorf("phase prompt should not be truncated but was")
	}

	if foundPhasePrompt.Content != expectedPrompt {
		t.Errorf("phase prompt content mismatch: got %d bytes, want %d bytes",
			len(foundPhasePrompt.Content), len(expectedPrompt))
	}

	var phasePromptMsgIdx int
	found := false
	for i, msg := range assembly.Messages {
		if strings.Contains(msg.Content, "Phase prompt content") {
			phasePromptMsgIdx = i
			found = true
			break
		}
	}

	if !found {
		t.Fatal("phase prompt message not found")
	}

	if assembly.Messages[phasePromptMsgIdx].Role != provider.MessageRoleSystem {
		t.Errorf("phase prompt message role = %q, want system",
			assembly.Messages[phasePromptMsgIdx].Role)
	}

	convMsgIdx := -1
	for i, msg := range assembly.Messages {
		if msg.Content == "hello" {
			convMsgIdx = i
			break
		}
	}

	if convMsgIdx < 0 {
		t.Fatal("conversation message not found")
	}

	if phasePromptMsgIdx >= convMsgIdx {
		t.Errorf("phase prompt message at index %d should come before conversation at index %d",
			phasePromptMsgIdx, convMsgIdx)
	}
}

func mustWrite(t *testing.T, dir, name, content string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func messageIndexByContent(t *testing.T, messages []provider.Message, want string) int {
	t.Helper()

	for i, message := range messages {
		if message.Content == want {
			return i
		}
	}
	t.Fatalf("message with content %q not found", want)
	return -1
}

func messageIndexByNameContains(t *testing.T, messages []provider.Message, want string) int {
	t.Helper()

	for i, message := range messages {
		if strings.Contains(message.Name, want) {
			return i
		}
	}
	t.Fatalf("message with name containing %q not found", want)
	return -1
}

// TestAssembleOrchestratorRoleReachesOrchestratorsOnly drives full prompt assembly with the
// three option combinations that occur in production and asserts the orchestrator role prose
// reaches exactly the callers that orchestrate.
//
// This is the assembly-level counterpart to the preamble-level gate tests in system_test.go:
// it exercises the real Assemble path rather than buildSystemPreamble directly, so a
// regression in how AssemblyOptions feeds the preamble is caught here.
//
// Each case mirrors a production call site; the comments name it so drift stays visible.
func TestAssembleOrchestratorRoleReachesOrchestratorsOnly(t *testing.T) {
	t.Parallel()

	const roleMarker = "not the default implementation worker"

	tests := []struct {
		name     string
		mirrors  string
		mutate   func(*AssemblyOptions)
		wantRole bool
	}{
		{
			name:    "interactive parent",
			mirrors: "cliRunner.promptAssembly (cmd/steiner/runner_run.go) with cfg.SubAgent.Enabled true",
			mutate: func(opts *AssemblyOptions) {
				opts.DelegationEnabled = true
				opts.WorkflowMode = ParentWorkflowMode()
			},
			wantRole: true,
		},
		{
			name: "oneshot phase",
			mirrors: "cliRunner.promptAssembly with WorkflowMode from phaseRunnerFactory " +
				"(cmd/steiner/cmd_oneshot.go), which is DelegatedChildWorkflowMode while " +
				"DelegationEnabled stays cfg.SubAgent.Enabled",
			mutate: func(opts *AssemblyOptions) {
				opts.DelegationEnabled = true
				opts.WorkflowMode = DelegatedChildWorkflowMode()
			},
			wantRole: true,
		},
		{
			name: "delegated child",
			mirrors: "buildChildPrompt (internal/delegation/bootstrap.go), which sets " +
				"WorkflowMode only and never sets DelegationEnabled",
			mutate: func(opts *AssemblyOptions) {
				opts.WorkflowMode = DelegatedChildWorkflowMode()
			},
			wantRole: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			projectRoot := t.TempDir()
			mustWrite(t, projectRoot, "go.mod", "module example.com/test\n")

			opts := AssemblyOptions{
				HomeDir:      t.TempDir(),
				ProjectRoot:  projectRoot,
				Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "do the thing"}},
			}
			tt.mutate(&opts)

			assembly, err := Assemble(context.Background(), opts)
			if err != nil {
				t.Fatalf("Assemble() error = %v", err)
			}

			preamble := blockContentBySource(t, assembly.Blocks, ContextSourcePreamble)

			if got := strings.Contains(preamble, roleMarker); got != tt.wantRole {
				t.Errorf("preamble contains %q = %t, want %t (mirrors %s)", roleMarker, got, tt.wantRole, tt.mirrors)
			}

			if !strings.Contains(preamble, identity) {
				t.Errorf("preamble missing identity %q; identity is shared by every agent and must always render", identity)
			}
		})
	}
}

func blockContentBySource(t *testing.T, blocks []ContextBlock, source ContextSource) string {
	t.Helper()

	for _, block := range blocks {
		if block.Source == source {
			return block.Content
		}
	}
	t.Fatalf("no block with source %q found", source)
	return ""
}
