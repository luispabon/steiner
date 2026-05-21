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

	homeDir := t.TempDir()
	mustWrite(t, filepath.Join(homeDir, ".config", "steiner"), "AGENTS.md", "global agents content")

	assembly, err := Assemble(context.Background(), AssemblyOptions{
		HomeDir: homeDir,
		Policy: AssemblyPolicy{
			Budgets: SourceBudgetModel{
				PreambleBytes:     5,
				GlobalAgentsBytes: 4,
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

	if got, want := assembly.Blocks[0].Content, "You a"; got != want {
		t.Fatalf("preamble block content = %q, want %q", got, want)
	}
	if !assembly.Blocks[0].Truncated {
		t.Fatalf("expected preamble block to be truncated")
	}
	if got, want := assembly.Blocks[0].ByteSize, 5; got != want {
		t.Fatalf("preamble block bytes = %d, want %d", got, want)
	}

	if got, want := assembly.Blocks[1].Content, "glob"; got != want {
		t.Fatalf("agent block content = %q, want %q", got, want)
	}
	if !assembly.Blocks[1].Truncated {
		t.Fatalf("expected agent block to be truncated")
	}
	if got, want := assembly.Blocks[1].ByteSize, 4; got != want {
		t.Fatalf("agent block bytes = %d, want %d", got, want)
	}

	if got, want := len(assembly.Messages), 1; got != want {
		t.Fatalf("len(messages) = %d, want %d", got, want)
	}

	wantContent := "You a\n\nglob"
	if got := assembly.Messages[0].Content; got != wantContent {
		t.Fatalf("merged system message content = %q, want %q", got, wantContent)
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

func TestBuildConversationCompactionPromptUsesFixedHeadings(t *testing.T) {
	t.Parallel()

	promptMessages := BuildConversationCompactionPrompt([]provider.Message{
		{Role: provider.MessageRoleUser, Content: "please keep the request intent"},
		{Role: provider.MessageRoleAssistant, Content: "solution design is to add compaction"},
		{Role: provider.MessageRoleUser, Content: "what should we do next?"},
	}, DurableContextState{
		RetainedSummaries: []DurableSummaryEntry{
			{Title: "prior work", Text: "do not drop constraints", Source: "user", Turn: 1},
		},
	}, "", CompactionModeNormal)
	if got, want := len(promptMessages), 2; got != want {
		t.Fatalf("prompt messages = %d, want %d", got, want)
	}
	if got, want := promptMessages[0].Role, provider.MessageRoleSystem; got != want {
		t.Fatalf("system role = %q, want %q", got, want)
	}
	for _, heading := range []string{
		"## 1. Task and Goal",
		"## 2. Current Repository / Project State",
		"## 3. Work Completed",
		"## 4. Key Findings and Decisions",
		"## 5. Problems Encountered",
		"## 6. Remaining Work",
	} {
		if got := strings.Contains(promptMessages[0].Content, heading); !got {
			t.Fatalf("system prompt = %q, want heading %q", promptMessages[0].Content, heading)
		}
	}
	if got := strings.Contains(promptMessages[1].Content, "durable context:"); !got {
		t.Fatalf("user prompt = %q, want durable context section", promptMessages[1].Content)
	}
}

func TestBuildConversationCompactionPromptEmergencyModeIsShorterAndLossier(t *testing.T) {
	t.Parallel()

	promptMessages := BuildConversationCompactionPrompt([]provider.Message{
		{Role: provider.MessageRoleUser, Content: "keep only what matters"},
	}, DurableContextState{}, "", CompactionModeEmergency)
	if got, want := len(promptMessages), 2; got != want {
		t.Fatalf("prompt messages = %d, want %d", got, want)
	}
	if got := strings.Contains(promptMessages[0].Content, "emergency handoff"); !got {
		t.Fatalf("system prompt = %q, want emergency handoff instruction", promptMessages[0].Content)
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
