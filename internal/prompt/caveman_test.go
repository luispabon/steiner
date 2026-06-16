package prompt

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestSystemPreambleCaveHumanModeEnabled(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", true, true, "").Content
	if !strings.Contains(content, testCaveHumanMarker) {
		t.Fatalf("cave-human preamble missing output voice block in %q", content)
	}
	if !strings.Contains(content, testTerseMarker) {
		t.Fatalf("cave-human preamble missing terse instruction in %q", content)
	}
	if !strings.Contains(content, testHumanMarker) {
		t.Fatalf("cave-human preamble missing human-style instruction in %q", content)
	}
}

func TestSystemPreambleCaveHumanModeDisabled(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", true, false, "").Content
	if strings.Contains(content, testCaveHumanMarker) {
		t.Fatalf("cave-human preamble contains output voice block when disabled in %q", content)
	}
	if strings.Contains(content, testTerseMarker) {
		t.Fatalf("cave-human preamble contains terse instruction when disabled in %q", content)
	}
	if strings.Contains(content, testHumanMarker) {
		t.Fatalf("cave-human preamble contains human-style instruction when disabled in %q", content)
	}
}

func TestSystemPreambleCaveHumanAppendedAfterBaseSections(t *testing.T) {
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
			content := SystemPreamble("", tc.delegation, true, tc.suffix).Content
			coreIdx := strings.Index(content, testCoreRulesMarker)
			workflowIdx := strings.Index(content, testWorkflowMarker)
			caveHumanIdx := strings.Index(content, testCaveHumanMarker)
			terseIdx := strings.Index(content, testTerseMarker)
			humanIdx := strings.Index(content, testHumanMarker)

			if coreIdx == -1 || workflowIdx == -1 || caveHumanIdx == -1 || terseIdx == -1 || humanIdx == -1 {
				t.Fatalf("missing section marker in %q", content)
			}
			if coreIdx >= workflowIdx {
				t.Fatalf("core rules section should appear before workflow section in %q", content)
			}
			if workflowIdx >= caveHumanIdx {
				t.Fatalf("cave-human instruction should appear after workflow section in %q", content)
			}
			if caveHumanIdx >= terseIdx {
				t.Fatalf("output voice block should appear before terse instruction in %q", content)
			}
			if terseIdx >= humanIdx {
				t.Fatalf("terse instruction should appear before human-style instruction in %q", content)
			}
			if tc.suffix != "" {
				suffixIdx := strings.Index(content, tc.suffix)
				if suffixIdx == -1 {
					t.Fatalf("suffix not found in %q", content)
				}
				if humanIdx >= suffixIdx {
					t.Fatalf("cave-human instruction should appear before suffix in %q", content)
				}
				if !strings.HasSuffix(strings.TrimSpace(content), tc.suffix) {
					t.Fatalf("suffix should be last in %q", content)
				}
			}
		})
	}
}

func TestBuildConversationCompactionPromptCaveHumanModeEnabled(t *testing.T) {
	t.Parallel()

	messages := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "hello"},
		{Role: provider.MessageRoleAssistant, Content: "hi"},
	}
	result := BuildConversationCompactionPrompt(messages, DurableContextState{}, "", CompactionModeNormal, true)
	if len(result) == 0 {
		t.Fatal("BuildConversationCompactionPrompt returned empty result")
	}
	sysContent := result[0].Content
	if !strings.Contains(sysContent, "compact working context for coding agent") {
		t.Fatalf("cave-human compaction prompt missing caveman body in %q", sysContent)
	}
	if !strings.Contains(sysContent, testCaveHumanMarker) {
		t.Fatalf("cave-human compaction prompt missing output voice block in %q", sysContent)
	}
	if !strings.Contains(sysContent, testTerseMarker) {
		t.Fatalf("cave-human compaction prompt missing terse instruction in %q", sysContent)
	}
	if !strings.Contains(sysContent, testHumanMarker) {
		t.Fatalf("cave-human compaction prompt missing human-style instruction in %q", sysContent)
	}
}

func TestBuildConversationCompactionPromptCaveHumanModeDisabled(t *testing.T) {
	t.Parallel()

	messages := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "hello"},
		{Role: provider.MessageRoleAssistant, Content: "hi"},
	}
	result := BuildConversationCompactionPrompt(messages, DurableContextState{}, "", CompactionModeNormal, false)
	if len(result) == 0 {
		t.Fatal("BuildConversationCompactionPrompt returned empty result")
	}
	sysContent := result[0].Content
	if strings.Contains(sysContent, "compact working context for coding agent") {
		t.Fatalf("non-cave-human compaction prompt should not contain caveman body in %q", sysContent)
	}
	if strings.Contains(sysContent, testCaveHumanMarker) {
		t.Fatalf("non-cave-human compaction prompt should not contain output voice block in %q", sysContent)
	}
}
