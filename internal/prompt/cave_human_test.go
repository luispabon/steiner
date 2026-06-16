package prompt

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

const (
	testTerseMarker = "Be terse."
	testHumanMarker = "Write like a person"
	testBodyMarker  = "compact working context for coding agent"
)

var testAntiTellMarkers = []string{
	"Banned vocabulary:",
	"No rule-of-three lists.",
}

func TestSystemPreambleCaveHumanEnabledAndDisabled(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		caveHuman   bool
		wantPresent []string
		wantAbsent  []string
	}{
		{
			name:      "enabled",
			caveHuman: true,
			wantPresent: []string{
				testCaveHumanMarker,
				testTerseMarker,
				testHumanMarker,
			},
		},
		{
			name:      "disabled",
			caveHuman: false,
			wantAbsent: []string{
				testCaveHumanMarker,
				testTerseMarker,
				testHumanMarker,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			content := SystemPreamble("", true, tc.caveHuman, "").Content
			for _, want := range tc.wantPresent {
				if !strings.Contains(content, want) {
					t.Fatalf("system preamble missing %q in %q", want, content)
				}
			}
			for _, forbidden := range tc.wantAbsent {
				if strings.Contains(content, forbidden) {
					t.Fatalf("system preamble unexpectedly contains %q in %q", forbidden, content)
				}
			}
		})
	}
}

func TestSystemPreambleCaveHumanComesAfterWorkflowAndBeforeSuffix(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", true, true, "custom suffix").Content
	workflowIdx := strings.Index(content, testWorkflowMarker)
	caveHumanIdx := strings.Index(content, testCaveHumanMarker)
	suffixIdx := strings.Index(content, "custom suffix")

	if workflowIdx == -1 {
		t.Fatal("workflow marker not found in content")
	}
	if caveHumanIdx == -1 {
		t.Fatal("cave-human marker not found in content")
	}
	if suffixIdx == -1 {
		t.Fatal("suffix not found in content")
	}
	if workflowIdx >= caveHumanIdx {
		t.Fatalf("cave-human block should appear after workflow block in %q", content)
	}
	if caveHumanIdx >= suffixIdx {
		t.Fatalf("cave-human block should appear before suffix in %q", content)
	}
	if !strings.HasSuffix(strings.TrimSpace(content), "custom suffix") {
		t.Fatalf("suffix should be last in %q", content)
	}
}

func TestRenderConversationCompactionInstructionCaveHuman(t *testing.T) {
	t.Parallel()

	enabled := RenderConversationCompactionInstruction("", CompactionModeNormal, true)
	for _, want := range []string{
		testBodyMarker,
		testCaveHumanMarker,
		testTerseMarker,
		testHumanMarker,
	} {
		if !strings.Contains(enabled, want) {
			t.Fatalf("enabled compaction instruction missing %q in %q", want, enabled)
		}
	}
	for _, want := range testAntiTellMarkers {
		if !strings.Contains(enabled, want) {
			t.Fatalf("enabled compaction instruction missing anti-tell %q in %q", want, enabled)
		}
	}

	disabled := RenderConversationCompactionInstruction("", CompactionModeNormal, false)
	for _, forbidden := range []string{
		testCaveHumanMarker,
		testTerseMarker,
		testHumanMarker,
		testBodyMarker,
		testAntiTellMarkers[0],
		testAntiTellMarkers[1],
	} {
		if strings.Contains(disabled, forbidden) {
			t.Fatalf("disabled compaction instruction unexpectedly contains %q in %q", forbidden, disabled)
		}
	}
}

func TestBuildConversationCompactionPromptCaveHuman(t *testing.T) {
	t.Parallel()

	messages := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "hello"},
		{Role: provider.MessageRoleAssistant, Content: "hi"},
	}

	enabled := BuildConversationCompactionPrompt(messages, DurableContextState{}, "", CompactionModeNormal, true)
	if len(enabled) == 0 {
		t.Fatal("BuildConversationCompactionPrompt returned empty result")
	}
	sysContent := enabled[0].Content
	for _, want := range []string{
		testBodyMarker,
		testCaveHumanMarker,
		testTerseMarker,
		testHumanMarker,
	} {
		if !strings.Contains(sysContent, want) {
			t.Fatalf("enabled compaction prompt missing %q in %q", want, sysContent)
		}
	}
	for _, want := range testAntiTellMarkers {
		if !strings.Contains(sysContent, want) {
			t.Fatalf("enabled compaction prompt missing anti-tell %q in %q", want, sysContent)
		}
	}

	disabled := BuildConversationCompactionPrompt(messages, DurableContextState{}, "", CompactionModeNormal, false)
	if len(disabled) == 0 {
		t.Fatal("BuildConversationCompactionPrompt returned empty result")
	}
	sysContent = disabled[0].Content
	for _, forbidden := range []string{
		testCaveHumanMarker,
		testTerseMarker,
		testHumanMarker,
		testBodyMarker,
		testAntiTellMarkers[0],
		testAntiTellMarkers[1],
	} {
		if strings.Contains(sysContent, forbidden) {
			t.Fatalf("disabled compaction prompt unexpectedly contains %q in %q", forbidden, sysContent)
		}
	}
}
