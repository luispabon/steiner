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

func TestSystemPreambleCavemanAppendedAfterBaseSections(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		delegation bool
		suffix     string
	}{
		{
			name: "without delegation",
		},
		{
			name:       "with delegation and suffix",
			delegation: true,
			suffix:     "custom suffix",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := SystemPreamble("", tc.delegation, true, false, tc.suffix).Content
			coreIdx := strings.Index(content, testCoreRulesMarker)
			workflowIdx := strings.Index(content, testWorkflowMarker)
			cavemanIdx := strings.Index(content, testCavemanMarker)

			if coreIdx == -1 || workflowIdx == -1 || cavemanIdx == -1 {
				t.Fatalf("missing section marker in %q", content)
			}
			if coreIdx >= workflowIdx {
				t.Fatalf("core rules section should appear before workflow section in %q", content)
			}
			if workflowIdx >= cavemanIdx {
				t.Fatalf("caveman instruction should appear after workflow section in %q", content)
			}
			if tc.suffix != "" {
				suffixIdx := strings.Index(content, tc.suffix)
				if suffixIdx == -1 {
					t.Fatalf("suffix not found in %q", content)
				}
				if cavemanIdx >= suffixIdx {
					t.Fatalf("caveman instruction should appear before suffix in %q", content)
				}
				if !strings.HasSuffix(strings.TrimSpace(content), tc.suffix) {
					t.Fatalf("suffix should be last in %q", content)
				}
			}
		})
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
