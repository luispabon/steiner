package prompt

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestSystemPreambleHumanizerModeEnabled(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", true, true, true, "").Content
	if !strings.Contains(content, "Write like a human") {
		t.Fatalf("humanizer mode preamble missing humanizer instruction in %q", content)
	}
}

func TestSystemPreambleHumanizerModeDisabled(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", true, true, false, "").Content
	if strings.Contains(content, "Write like a human") {
		t.Fatalf("humanizer mode preamble contains humanizer instruction when disabled in %q", content)
	}
}

func TestSystemPreambleHumanizerAfterCavemanOrder(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", false, true, true, "").Content
	cavemanIdx := strings.Index(content, "Respond terse")
	humanizerIdx := strings.Index(content, "Write like a human")

	if cavemanIdx == -1 {
		t.Fatal("caveman instruction not found in content")
	}
	if humanizerIdx == -1 {
		t.Fatal("humanizer instruction not found in content")
	}
	if cavemanIdx >= humanizerIdx {
		t.Fatalf("caveman block (index %d) should appear before humanizer block (index %d) in %q", cavemanIdx, humanizerIdx, content)
	}
}

func TestSystemPreambleHumanizerBeforeSuffixOrder(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", false, false, true, "Custom suffix").Content
	humanizerIdx := strings.Index(content, "Write like a human")
	suffixIdx := strings.Index(content, "Custom suffix")

	if humanizerIdx == -1 {
		t.Fatal("humanizer instruction not found in content")
	}
	if suffixIdx == -1 {
		t.Fatal("suffix not found in content")
	}
	if humanizerIdx >= suffixIdx {
		t.Fatalf("humanizer block (index %d) should appear before suffix (index %d) in %q", humanizerIdx, suffixIdx, content)
	}
	if !strings.HasSuffix(strings.TrimSpace(content), "Custom suffix") {
		t.Fatal("suffix should be at the end of preamble")
	}
}

func TestBuildConversationCompactionPromptHumanizerModeEnabled(t *testing.T) {
	t.Parallel()

	messages := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "hello"},
		{Role: provider.MessageRoleAssistant, Content: "hi"},
	}
	result := BuildConversationCompactionPrompt(messages, DurableContextState{}, "", CompactionModeNormal, false, true)
	if len(result) == 0 {
		t.Fatal("BuildConversationCompactionPrompt returned empty result")
	}
	sysContent := result[0].Content
	// Humanizer instruction is present
	if !strings.Contains(sysContent, "Write like a human") {
		t.Fatalf("humanizer compaction prompt missing humanizer instruction in %q", sysContent)
	}
	// Body is still the standard compaction body (no humanizer-specific body variant)
	if strings.Contains(sysContent, "compact working context for coding agent") {
		t.Fatalf("humanizer compaction prompt should not contain caveman body in %q", sysContent)
	}
}

func TestBuildConversationCompactionPromptHumanizerModeDisabled(t *testing.T) {
	t.Parallel()

	messages := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "hello"},
		{Role: provider.MessageRoleAssistant, Content: "hi"},
	}
	result := BuildConversationCompactionPrompt(messages, DurableContextState{}, "", CompactionModeNormal, false, false)
	if len(result) == 0 {
		t.Fatal("BuildConversationCompactionPrompt returned empty result")
	}
	sysContent := result[0].Content
	if strings.Contains(sysContent, "Write like a human") {
		t.Fatalf("humanizer compaction prompt should not contain humanizer instruction when disabled in %q", sysContent)
	}
}
