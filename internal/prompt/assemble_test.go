package prompt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

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
		SkillsRoot:                skillsRoot,
		Conversation:              []provider.Message{{Role: provider.MessageRoleUser, Content: "how do I fix this?"}, {Role: provider.MessageRoleAssistant, Content: "use the tools"}},
		ToolResults:               []provider.Message{{Role: provider.MessageRoleTool, Content: "tool result"}},
		ProjectContextBudgetBytes: 1024,
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

	if got, want := len(assembly.Messages), 8; got != want {
		t.Fatalf("len(messages) = %d, want %d", got, want)
	}

	if got := assembly.Messages[0].Role; got != provider.MessageRoleSystem {
		t.Fatalf("message[0].role = %q, want system", got)
	}
	if got, want := assembly.Messages[1].Content, "global rules"; got != want {
		t.Fatalf("message[1].content = %q, want %q", got, want)
	}
	if got, want := assembly.Messages[2].Content, "project rules"; got != want {
		t.Fatalf("message[2].content = %q, want %q", got, want)
	}
	if got := assembly.Messages[3].Role; got != provider.MessageRoleUser {
		t.Fatalf("message[3].role = %q, want user", got)
	}
	if got := strings.Contains(assembly.Messages[3].Name, "README.md"); !got {
		t.Fatalf("message[3].name = %q, want README.md path", assembly.Messages[3].Name)
	}
	if got, want := assembly.Messages[4].Content, "module example.com/test\n"; got != want {
		t.Fatalf("message[4].content = %q, want %q", got, want)
	}
	if got, want := assembly.Messages[5].Content, "how do I fix this?"; got != want {
		t.Fatalf("message[5].content = %q, want %q", got, want)
	}
	if got := strings.Contains(assembly.Messages[7].Content, "\"kind\":\"tool_summary\""); !got {
		t.Fatalf("message[7].content = %q, want tool summary envelope", assembly.Messages[7].Content)
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
		SkillsRoot:                skillsRoot,
		SkillNames:                []string{"codex"},
		ProjectContextBudgetBytes: 1,
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	foundSkill := false
	for _, block := range assembly.Blocks {
		if block.Source == ContextSourceSkill {
			foundSkill = true
			if got, want := block.Content, "skill instructions"; got != want {
				t.Fatalf("skill block content = %q, want %q", got, want)
			}
		}
	}
	if !foundSkill {
		t.Fatalf("expected skill block to be present")
	}

	// The skill should appear after the project context and before any conversation.
	gotIndex := -1
	for i, message := range assembly.Messages {
		if message.Content == "skill instructions" {
			gotIndex = i
			break
		}
	}
	if gotIndex < 0 {
		t.Fatalf("skill message not found")
	}
	if gotIndex == 0 || assembly.Messages[gotIndex-1].Role != provider.MessageRoleSystem {
		t.Fatalf("skill message not placed after system context")
	}
}

func TestGatherProjectContextHonorsBudget(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	mustWrite(t, projectRoot, "README.md", "1234567890")
	mustWrite(t, projectRoot, "go.mod", "module example.com/test\n")

	blocks, err := GatherProjectContext(ProjectContextOptions{
		Root:        projectRoot,
		BudgetBytes: 5,
	})
	if err != nil {
		t.Fatalf("GatherProjectContext() error = %v", err)
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

func TestAssembleCompactsOlderTurnsAndBoundsGrowth(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	policy := AssemblyPolicy{
		Retention:   RetentionPolicy{RecentTurns: 2},
		Compaction:  CompactionPolicy{SummaryBytes: 512},
		ToolSummary: ToolSummaryPolicy{MaxBytes: 24},
	}

	shortAssembly, err := Assemble(context.Background(), AssemblyOptions{
		ProjectRoot:               projectRoot,
		ProjectContextBudgetBytes: 1,
		Policy:                    policy,
		Conversation: []provider.Message{
			{Role: provider.MessageRoleUser, Content: "turn one user"},
			{Role: provider.MessageRoleAssistant, Content: "turn one assistant"},
			{Role: provider.MessageRoleUser, Content: "turn two user"},
			{Role: provider.MessageRoleAssistant, Content: "turn two assistant"},
			{Role: provider.MessageRoleUser, Content: "turn three user"},
			{Role: provider.MessageRoleAssistant, Content: "turn three assistant"},
		},
		ToolResults: []provider.Message{{Role: provider.MessageRoleTool, Content: "tool result content that is much longer than the summary budget"}},
	})
	if err != nil {
		t.Fatalf("short Assemble() error = %v", err)
	}

	longAssembly, err := Assemble(context.Background(), AssemblyOptions{
		ProjectRoot:               projectRoot,
		ProjectContextBudgetBytes: 1,
		Policy:                    policy,
		Conversation: []provider.Message{
			{Role: provider.MessageRoleUser, Content: "turn zero user"},
			{Role: provider.MessageRoleAssistant, Content: "turn zero assistant"},
			{Role: provider.MessageRoleUser, Content: "turn one user"},
			{Role: provider.MessageRoleAssistant, Content: "turn one assistant"},
			{Role: provider.MessageRoleUser, Content: "turn two user"},
			{Role: provider.MessageRoleAssistant, Content: "turn two assistant"},
			{Role: provider.MessageRoleUser, Content: "turn three user"},
			{Role: provider.MessageRoleAssistant, Content: "turn three assistant"},
			{Role: provider.MessageRoleUser, Content: "turn four user"},
			{Role: provider.MessageRoleAssistant, Content: "turn four assistant"},
			{Role: provider.MessageRoleUser, Content: "turn five user"},
			{Role: provider.MessageRoleAssistant, Content: "turn five assistant"},
		},
		ToolResults: []provider.Message{{Role: provider.MessageRoleTool, Content: "tool result content that is much longer than the summary budget"}},
	})
	if err != nil {
		t.Fatalf("long Assemble() error = %v", err)
	}

	if got, want := len(shortAssembly.Messages), len(longAssembly.Messages); got != want {
		t.Fatalf("bounded assembly length mismatch: short=%d long=%d", got, want)
	}
	if got, want := len(longAssembly.Messages), 7; got != want {
		t.Fatalf("len(long messages) = %d, want %d", got, want)
	}

	summaryBlock := findBlockBySource(t, longAssembly.Blocks, ContextSourceConversationSummary)
	var convoSummary ConversationSummaryEnvelope
	if err := json.Unmarshal([]byte(summaryBlock.Content), &convoSummary); err != nil {
		t.Fatalf("unmarshal conversation summary: %v", err)
	}
	if got, want := convoSummary.DroppedTurns, 4; got != want {
		t.Fatalf("conversation summary dropped turns = %d, want %d", got, want)
	}
	if got, want := convoSummary.DroppedMessages, 8; got != want {
		t.Fatalf("conversation summary dropped messages = %d, want %d", got, want)
	}
	if got := strings.Contains(summaryBlock.Content, "turn zero user"); !got {
		t.Fatalf("conversation summary = %q, want excerpt from older turns", summaryBlock.Content)
	}

	if got, want := shortAssembly.Messages[2].Content, "turn two user"; got != want {
		t.Fatalf("retained user turn content = %q, want %q", got, want)
	}
	if got, want := shortAssembly.Messages[3].Content, "turn two assistant"; got != want {
		t.Fatalf("retained assistant turn content = %q, want %q", got, want)
	}
	if got := strings.Contains(longAssembly.Messages[6].Content, "\"kind\":\"tool_summary\""); !got {
		t.Fatalf("tool summary envelope missing: %q", longAssembly.Messages[6].Content)
	}
	if got := strings.Contains(longAssembly.Messages[6].Content, "\"truncated\":true"); !got {
		t.Fatalf("tool summary should be truncated: %q", longAssembly.Messages[6].Content)
	}
}

func TestAssembleSnapshotCapturesCompactedPromptFixture(t *testing.T) {
	t.Parallel()

	fixtureRoot := filepath.Join("..", "..", "testdata", "stage3", "compaction_fixture")
	conversation := loadMessagesFixture(t, filepath.Join(fixtureRoot, "conversation.json"))

	homeDir := t.TempDir()
	mustWrite(t, filepath.Join(homeDir, ".config", "steiner"), "AGENTS.md", "global rules")

	assembly, err := Assemble(context.Background(), AssemblyOptions{
		HomeDir:                   homeDir,
		ProjectRoot:               fixtureRoot,
		ProjectContextBudgetBytes: 128,
		Conversation:              conversation,
		Policy: AssemblyPolicy{
			Retention:  RetentionPolicy{RecentTurns: 2},
			Compaction: CompactionPolicy{SummaryBytes: 1024},
		},
		ContextState: DurableContextState{
			ActiveConstraints: []DurableContextEntry{{Text: "do not change public APIs", Source: "user", Turn: 1}},
			UnresolvedWork:    []DurableContextEntry{{Text: "tighten retry handling", Source: "assistant", Turn: 2}},
			ActiveFocus:       &DurableContextEntry{Text: "finish compaction diagnostics", Source: "assistant", Turn: 3},
		},
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	if got, want := len(assembly.Messages), 11; got != want {
		t.Fatalf("len(messages) = %d, want %d", got, want)
	}
	if got, want := len(assembly.Blocks), 7; got != want {
		t.Fatalf("len(blocks) = %d, want %d", got, want)
	}

	got := renderAssemblyBlocksSnapshot(assembly.Blocks)
	want, err := os.ReadFile(filepath.Join(fixtureRoot, "assembly_blocks.snapshot"))
	if err != nil {
		t.Fatalf("ReadFile(snapshot) error = %v", err)
	}
	if got, want := strings.TrimSpace(got), strings.TrimSpace(string(want)); got != want {
		t.Fatalf("assembly snapshot mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestCompactConversationTurnsMarksTruncation(t *testing.T) {
	t.Parallel()

	block, ok := CompactConversationTurns([]conversationTurn{
		{Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: strings.Repeat("x", 200)}}},
	}, CompactionPolicy{SummaryBytes: 32})
	if !ok {
		t.Fatalf("CompactConversationTurns() ok = false, want true")
	}
	if !block.Truncated {
		t.Fatalf("summary block truncated = false, want true")
	}

	var envelope ConversationSummaryEnvelope
	if err := json.Unmarshal([]byte(block.Content), &envelope); err != nil {
		t.Fatalf("unmarshal summary envelope: %v", err)
	}
	if !envelope.Truncated {
		t.Fatalf("envelope truncated = false, want true")
	}
}

func findBlockBySource(t *testing.T, blocks []ContextBlock, source ContextSource) ContextBlock {
	t.Helper()

	for _, block := range blocks {
		if block.Source == source {
			return block
		}
	}
	t.Fatalf("block with source %q not found", source)
	return ContextBlock{}
}

func loadMessagesFixture(t *testing.T, path string) []provider.Message {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	var messages []provider.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", path, err)
	}
	return messages
}

func renderAssemblyBlocksSnapshot(blocks []ContextBlock) string {
	var builder strings.Builder
	for _, block := range blocks {
		if block.Source == ContextSourcePreamble {
			continue
		}
		fmt.Fprintf(&builder, "source=%s path=%s truncated=%t content=%s\n",
			block.Source,
			filepath.Base(block.Path),
			block.Truncated,
			renderSnapshotContent(block),
		)
	}
	return strings.TrimSpace(builder.String())
}

func renderSnapshotContent(block ContextBlock) string {
	content := block.Content
	switch block.Source {
	case ContextSourceConversationSummary, ContextSourceDurableContext, ContextSourceToolSummary:
		var envelope struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(block.Content), &envelope); err == nil && envelope.Content != "" {
			content = envelope.Content
		}
	}
	return normalizeSnapshotText(content)
}

func normalizeSnapshotText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
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
