package prompt

import (
	"context"
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

	if got, want := len(assembly.Blocks), 5; got != want {
		t.Fatalf("len(blocks) = %d, want %d", got, want)
	}

	wantSources := []ContextSource{
		ContextSourcePreamble,
		ContextSourceGlobalAgentsMD,
		ContextSourceProjectAgentsMD,
		ContextSourceProjectContext,
		ContextSourceProjectContext,
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
	if got, want := assembly.Messages[7].Content, "tool result"; got != want {
		t.Fatalf("message[7].content = %q, want %q", got, want)
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
