package prompt

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestSystemPreambleCaveHumanModeEnabled_HumanizerChecks(t *testing.T) {
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

func TestSystemPreambleCaveHumanModeDisabled_HumanizerChecks(t *testing.T) {
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

func TestSystemPreambleCaveHumanOrder(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", false, true, "").Content
	cavemanIdx := strings.Index(content, testTerseMarker)
	humanizerIdx := strings.Index(content, testHumanMarker)

	if cavemanIdx == -1 {
		t.Fatal("caveman instruction not found in content")
	}
	if humanizerIdx == -1 {
		t.Fatal("humanizer instruction not found in content")
	}
	if cavemanIdx >= humanizerIdx {
		t.Fatalf("terse block (index %d) should appear before humanizer block (index %d) in %q", cavemanIdx, humanizerIdx, content)
	}
}

func TestSystemPreambleCaveHumanBeforeSuffixOrder(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", false, true, "Custom suffix").Content
	humanizerIdx := strings.Index(content, testHumanMarker)
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

func TestSystemPreambleCaveHumanAppendedBeforeSuffix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		delegation bool
		caveHuman  bool
		suffix     string
	}{
		{
			name: "without delegation",
		},
		{
			name:       "with delegation",
			delegation: true,
			caveHuman:  true,
			suffix:     "custom suffix",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := SystemPreamble("", tc.delegation, tc.caveHuman, tc.suffix).Content
			coreIdx := strings.Index(content, testCoreRulesMarker)
			workflowIdx := strings.Index(content, testWorkflowMarker)
			humanizerIdx := strings.Index(content, testHumanMarker)
			if coreIdx == -1 || workflowIdx == -1 {
				t.Fatalf("core or workflow marker missing in %q", content)
			}
			if coreIdx >= workflowIdx {
				t.Fatalf("core rules section should appear before workflow section in %q", content)
			}
			if tc.caveHuman {
				if coreIdx == -1 || workflowIdx == -1 || humanizerIdx == -1 {
					t.Fatalf("missing section marker in %q", content)
				}
				if workflowIdx >= humanizerIdx {
					t.Fatalf("human-style instruction should appear after workflow section in %q", content)
				}
				cavemanIdx := strings.Index(content, testTerseMarker)
				if cavemanIdx == -1 {
					t.Fatalf("terse instruction not found in %q", content)
				}
				if cavemanIdx >= humanizerIdx {
					t.Fatalf("terse instruction should appear before humanizer instruction in %q", content)
				}
			} else {
				if humanizerIdx != -1 {
					t.Fatalf("human-style instruction unexpectedly present in %q", content)
				}
				if strings.Contains(content, testCaveHumanMarker) {
					t.Fatalf("output voice block unexpectedly present in %q", content)
				}
			}
			if tc.suffix != "" {
				suffixIdx := strings.Index(content, tc.suffix)
				if suffixIdx == -1 {
					t.Fatalf("suffix not found in %q", content)
				}
				if humanizerIdx >= suffixIdx {
					t.Fatalf("human-style instruction should appear before suffix in %q", content)
				}
				if !strings.HasSuffix(strings.TrimSpace(content), tc.suffix) {
					t.Fatalf("suffix should be last in %q", content)
				}
			}
		})
	}
}

func TestBuildConversationCompactionPromptCaveHumanModeEnabled_HumanizerChecks(t *testing.T) {
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
	if !strings.Contains(sysContent, testCaveHumanMarker) {
		t.Fatalf("cave-human compaction prompt missing output voice block in %q", sysContent)
	}
	if !strings.Contains(sysContent, testTerseMarker) {
		t.Fatalf("cave-human compaction prompt missing terse instruction in %q", sysContent)
	}
	if !strings.Contains(sysContent, testHumanMarker) {
		t.Fatalf("cave-human compaction prompt missing human-style instruction in %q", sysContent)
	}
	if !strings.Contains(sysContent, "compact working context for coding agent") {
		t.Fatalf("cave-human compaction prompt should contain caveman body in %q", sysContent)
	}
}

func TestBuildConversationCompactionPromptCaveHumanModeDisabled_HumanizerChecks(t *testing.T) {
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
	if strings.Contains(sysContent, testCaveHumanMarker) {
		t.Fatalf("cave-human compaction prompt should not contain output voice block when disabled in %q", sysContent)
	}
}
