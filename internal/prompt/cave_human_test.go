package prompt

import (
	"strings"
	"testing"
)

const (
	testTerseMarker     = "Be terse."
	testHumanMarker     = "Write like a person"
	testStructureMarker = "Terseness governs prose, not structure."
	testBodyMarker      = "compact working context for coding agent"
	testEncodingMarker  = "Encoding directives:"
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
				testStructureMarker,
			},
		},
		{
			name:      "disabled",
			caveHuman: false,
			wantAbsent: []string{
				testCaveHumanMarker,
				testTerseMarker,
				testHumanMarker,
				testStructureMarker,
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

	enabled := RenderConversationCompactionInstruction("", CompactionModeNormal, true, "")
	for _, want := range []string{testBodyMarker, testEncodingMarker} {
		if !strings.Contains(enabled, want) {
			t.Fatalf("enabled compaction instruction missing %q in %q", want, enabled)
		}
	}
	for _, forbidden := range []string{
		testCaveHumanMarker,
		testTerseMarker,
		testHumanMarker,
		testAntiTellMarkers[0],
		testAntiTellMarkers[1],
	} {
		if strings.Contains(enabled, forbidden) {
			t.Fatalf("enabled compaction instruction unexpectedly contains generic output-voice marker %q in %q", forbidden, enabled)
		}
	}

	disabled := RenderConversationCompactionInstruction("", CompactionModeNormal, false, "")
	for _, forbidden := range []string{
		testCaveHumanMarker,
		testTerseMarker,
		testHumanMarker,
		testBodyMarker,
		testEncodingMarker,
		testAntiTellMarkers[0],
		testAntiTellMarkers[1],
	} {
		if strings.Contains(disabled, forbidden) {
			t.Fatalf("disabled compaction instruction unexpectedly contains %q in %q", forbidden, disabled)
		}
	}
}
