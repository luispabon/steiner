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

func TestAssembleCarriesRetainedSummariesIntoDurableContext(t *testing.T) {
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

	block := findBlockBySource(t, assembly.Blocks, ContextSourceDurableContext)
	if !strings.Contains(block.Content, "retained summaries") {
		t.Fatalf("durable context block = %q, want retained summaries section", block.Content)
	}
	if !strings.Contains(block.Content, "earlier request and tool output") {
		t.Fatalf("durable context block = %q, want retained summary text", block.Content)
	}
}

func TestBuildConversationCompactionPromptUsesFixedHeadings(t *testing.T) {
	t.Parallel()

	promptMessages := BuildConversationCompactionPrompt([]provider.Message{
		{Role: provider.MessageRoleUser, Content: "please keep the request intent"},
		{Role: provider.MessageRoleAssistant, Content: "solution design is to add compaction"},
		{Role: provider.MessageRoleUser, Content: "what should we do next?"},
	}, DurableContextState{
		ActiveConstraints: []DurableContextEntry{{Text: "do not drop constraints", Source: "user", Turn: 1}},
		UnresolvedWork:    []DurableContextEntry{{Text: "finish the compaction loop", Source: "assistant", Turn: 2}},
	})
	if got, want := len(promptMessages), 2; got != want {
		t.Fatalf("prompt messages = %d, want %d", got, want)
	}
	if got, want := promptMessages[0].Role, provider.MessageRoleSystem; got != want {
		t.Fatalf("system role = %q, want %q", got, want)
	}
	for _, heading := range []string{
		"# Request intent",
		"# Solution design",
		"# Recent actions",
		"# Unresolved decisions",
		"# Pending work",
	} {
		if got := strings.Contains(promptMessages[0].Content, heading); !got {
			t.Fatalf("system prompt = %q, want heading %q", promptMessages[0].Content, heading)
		}
	}
	if got := strings.Contains(promptMessages[1].Content, "durable context:"); !got {
		t.Fatalf("user prompt = %q, want durable context section", promptMessages[1].Content)
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
