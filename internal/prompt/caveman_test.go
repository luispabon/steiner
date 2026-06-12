package prompt

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestSystemPreambleCavemanModeEnabled(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", true, true, false, "").Content
	if !strings.Contains(content, "Respond terse") {
		t.Fatalf("caveman mode preamble missing terse instruction in %q", content)
	}
}

func TestSystemPreambleCavemanModeDisabled(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", true, false, false, "").Content
	if strings.Contains(content, "Respond terse") {
		t.Fatalf("caveman mode preamble contains terse instruction when disabled in %q", content)
	}
}

func TestBuildConversationCompactionPromptCavemanModeEnabled(t *testing.T) {
	t.Parallel()

	messages := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "hello"},
		{Role: provider.MessageRoleAssistant, Content: "hi"},
	}
	result := BuildConversationCompactionPrompt(messages, DurableContextState{}, "", CompactionModeNormal, true, false)
	if len(result) == 0 {
		t.Fatal("BuildConversationCompactionPrompt returned empty result")
	}
	sysContent := result[0].Content
	if !strings.Contains(sysContent, "compact working context for coding agent") {
		t.Fatalf("caveman compaction prompt missing caveman body in %q", sysContent)
	}
}

func TestBuildConversationCompactionPromptCavemanModeDisabled(t *testing.T) {
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
	if strings.Contains(sysContent, "compact working context for coding agent") {
		t.Fatalf("non-caveman compaction prompt should not contain caveman body in %q", sysContent)
	}
}
