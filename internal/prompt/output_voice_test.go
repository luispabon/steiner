package prompt

import (
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestSystemPreambleOutputVoiceAlwaysPresent(t *testing.T) {
	t.Parallel()

	for _, mode := range []WorkflowMode{ParentWorkflowMode(), DelegatedChildWorkflowMode(), DelegatedCodeSubAgentWorkflowMode(), DelegatedNonCodeChildWorkflowMode()} {
		for _, override := range []string{"", "custom system prompt"} {
			for _, delegation := range []bool{false, true} {
				content := SystemPreambleWithAdvisor(SystemPreambleParams{
					Mode: mode, Override: override, DelegationEnabled: delegation, SystemSuffix: "custom suffix",
				}).Content
				if got := strings.Count(content, testOutputVoiceMarker); got != 1 {
					t.Fatalf("mode=%s override=%q delegation=%t: output voice count = %d, want 1", mode, override, delegation, got)
				}
				if !strings.Contains(content, strings.TrimSpace(renderTemplate(templateOutputVoice, nil))) {
					t.Fatalf("mode=%s override=%q delegation=%t: output voice incomplete", mode, override, delegation)
				}
				voice := strings.Index(content, testOutputVoiceMarker)
				if override != "" && strings.Index(content, override) >= voice {
					t.Fatal("output voice must follow system override")
				}
				if voice >= strings.Index(content, "custom suffix") || !strings.HasSuffix(content, "custom suffix") {
					t.Fatal("system suffix must follow output voice and remain last")
				}
			}
		}
	}
}

func TestOutputVoiceRules(t *testing.T) {
	t.Parallel()

	voice := renderTemplate(templateOutputVoice, nil)
	for _, want := range []string{
		"Cut fluff, not substance.",
		`"use", not "leverage"`,
		`"is", not "serves as"`,
		"No padded rhetorical triples",
		"Keep genuine enumerations.",
		"Never emit an em dash (Unicode U+2014) or en dash (Unicode U+2013)",
		"Check every authored response and text written to files",
		"newly written comments and docstrings",
		"Preserve necessary explanations, caveats, and uncertainty.",
		"preserve existing verbatim source, quotations, and errors exactly",
		"Generated code is not a blanket exception",
	} {
		if !strings.Contains(voice, want) {
			t.Errorf("output voice missing %q", want)
		}
	}
	if strings.ContainsAny(voice, "\u2013\u2014") {
		t.Fatal("output voice must not demonstrate the prohibited dash characters")
	}
}

func TestAssembleIncludesOutputVoice(t *testing.T) {
	t.Parallel()

	for _, override := range []string{"", "custom system prompt"} {
		assembly, err := Assemble(context.Background(), AssemblyOptions{PromptOverrides: config.ModelPrompts{System: override}})
		if err != nil {
			t.Fatal(err)
		}
		if len(assembly.Messages) == 0 || !strings.Contains(assembly.Messages[0].Content, testOutputVoiceMarker) {
			t.Fatalf("assembled system message missing output voice with override=%q", override)
		}
	}
}

func TestCompactionPreservesHandoffWithoutOutputVoice(t *testing.T) {
	t.Parallel()

	content := RenderConversationCompactionInstruction("", CompactionModeNormal)
	for _, want := range []string{
		"1. Task and Goal:", "2. Current Repository State:", "3. Work Completed:",
		"4. Key Findings and Decisions:", "5. Problems Encountered:", "6. Remaining Work:",
		"7. Verification and Acceptance:", "8. User Preferences and Interaction Context:",
		"Encoding directives:", "Drop articles where meaning survives.",
		"Use compact clauses; semicolons are fine when clear.",
		"Keep full sentences where needed to avoid ambiguity.",
		"Preserve paths and identifiers exactly. Remove repeated mentions rather than abbreviating them.",
		"Use key=value notation", `No markdown headers: use "label:" prefix instead.`,
		"Omit a section entirely if it would be empty.",
		"Preserve exact paths", "Do not expose secrets.", "safest continuation point",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("compaction instruction missing %q", want)
		}
	}
	for _, forbidden := range []string{testOutputVoiceMarker, "Use semicolons, not sentences.", "Abbreviate paths after first mention.", "\u2013", "\u2014"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("compaction instruction contains chat voice, unsafe encoding, or prohibited dash %q", forbidden)
		}
	}
}
