package prompt

import (
	"strings"
	"testing"
)

// segmentAppendInstructionWant pins the complete appended-segment instruction.
// The renderer trims the template, so this is the rendered text without the
// template's trailing newline.
const segmentAppendInstructionWant = `You extend the summary of a coding agent conversation. Another agent must resume from the full summary chain alone.

Earlier summary messages that contain a "Segment N." header are immutable segments. Steiner wraps them in its own summary framing, so read the header inside the message. They are read-only records: reuse them as written, and do not repeat their facts. The last summary message that contains a "Segment N." header is the newest segment. The conversation after it, up to the retained recent turns, is the new span. Summarize only the new span.

Ignore [steiner:history] lines and [steiner:latest-request ...] blocks. Steiner rebuilds them after this summary.

Cover the new span with these labels, and omit a label when the new span has nothing for it:
Task and Goal: progress on the user request, end state, success criteria, constraints, assumptions.
Current Repository State: files changed, code structure, uncommitted changes, external services.
Work Completed: what was implemented, commands run, tests executed, results, commits.
Key Findings and Decisions: technical discoveries, design choices, tradeoffs, dependencies.
Problems Encountered: errors, root causes, fixes, workarounds, rejected approaches.
Remaining Work: next actions, files to change, tests needed, blockers, open questions, risks.
Verification and Acceptance: commands that prove completion, expected outcomes, verification limits.
User Preferences and Interaction Context: style, conventions, promises, things to avoid repeating.

Rules:
- Be factual and specific.
- Preserve exact paths, filenames, symbols, commands, error messages, config keys, API names, versions and decisions.
- Do not repeat facts that an earlier segment already records.
- Do not expose secrets. Refer by purpose only.
- Do not include hidden reasoning. Summarize conclusions only.
- Do not claim completion unless implemented and verified.
- Reply with the summary body only. Do not write a Segment header; it is added for you.
- Write for immediate continuation by coding agent.

Supersession:
- If a fact in an earlier segment is now wrong or superseded, end the reply with a "## Corrections" heading.
- Under the heading, write one line per correction: "Segment K: <old fact> -> <new fact>", where K is the number in that segment's "Segment K." header.
- Omit the "## Corrections" heading when nothing needs correcting.`

// segmentAppendRulesWant is segmentAppendInstructionWant without the opening
// generic guidance paragraph, which a compaction override replaces.
func segmentAppendRulesWant(t *testing.T) string {
	t.Helper()
	_, rules, ok := strings.Cut(segmentAppendInstructionWant, "\n\n")
	if !ok {
		t.Fatal("append instruction literal holds no guidance paragraph separator")
	}
	return rules
}

func TestRenderSegmentAppendInstructionExact(t *testing.T) {
	t.Parallel()

	if got := RenderSegmentAppendInstruction("", ""); got != segmentAppendInstructionWant {
		t.Fatalf("RenderSegmentAppendInstruction() = %q, want %q", got, segmentAppendInstructionWant)
	}
}

// TestSegmentAppendInstructionSemantics pins the directives the agent relies
// on: earlier segments stay read-only, mutable tail markers are ignored, and
// superseded facts go under a Corrections heading.
func TestSegmentAppendInstructionSemantics(t *testing.T) {
	t.Parallel()

	got := RenderSegmentAppendInstruction("", "")
	for _, want := range []string{
		"immutable segments",
		"read-only records",
		"do not repeat their facts",
		"Summarize only the new span",
		"[steiner:history]",
		"[steiner:latest-request ...]",
		`"## Corrections" heading`,
		`"Segment K: <old fact> -> <new fact>"`,
		`Omit the "## Corrections" heading when nothing needs correcting.`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderSegmentAppendInstruction() = %q, want it to contain %q", got, want)
		}
	}
}

// TestRenderSegmentAppendInstructionOverrideKeepsAppendRules pins the override
// semantics: the override replaces the generic guidance, and the append rules
// follow it, so a custom prompt cannot make the segment chain mutable.
func TestRenderSegmentAppendInstructionOverrideKeepsAppendRules(t *testing.T) {
	t.Parallel()

	const override = "custom compaction prompt"
	got := RenderSegmentAppendInstruction(override, "")
	if want := override + "\n\n" + segmentAppendRulesWant(t); got != want {
		t.Fatalf("override instruction = %q, want %q", got, want)
	}
	if strings.Contains(got, "You extend the summary of a coding agent conversation") {
		t.Fatalf("override instruction kept the generic guidance: %q", got)
	}
	for _, rule := range []string{
		`contain a "Segment N." header are immutable segments`,
		"read-only records",
		"Summarize only the new span",
		"Reply with the summary body only. Do not write a Segment header; it is added for you.",
		`The last summary message that contains a "Segment N." header is the newest segment.`,
		`"## Corrections" heading`,
	} {
		if !strings.Contains(got, rule) {
			t.Errorf("override instruction = %q, want it to contain the append rule %q", got, rule)
		}
	}
}

func TestRenderSegmentAppendInstructionSteering(t *testing.T) {
	t.Parallel()

	const steering = "focus on auth refactor"
	const marker = "Additional user steering for this compaction:\n\n" + steering

	bare := RenderSegmentAppendInstruction("", "")
	if empty := RenderSegmentAppendInstruction("", ""); empty != bare {
		t.Fatalf("empty steering changed output: bare=%q empty=%q", bare, empty)
	}

	steered := RenderSegmentAppendInstruction("", steering)
	if want := bare + "\n\n" + marker; steered != want {
		t.Fatalf("steered instruction = %q, want %q", steered, want)
	}

	const override = "custom compaction prompt"
	if got, want := RenderSegmentAppendInstruction(override, steering), override+"\n\n"+segmentAppendRulesWant(t)+"\n\n"+marker; got != want {
		t.Fatalf("override plus steering = %q, want %q", got, want)
	}
}

// TestRenderConversationCompactionInstructionUnchangedByAppendTemplate pins the
// normal and emergency renders to their original composition, so adding the
// append template cannot drift them.
func TestRenderConversationCompactionInstructionUnchangedByAppendTemplate(t *testing.T) {
	t.Parallel()

	base := compactionPromptSystemInstruction() + "\n\n" + compactionPromptInstructionBody()
	normal := RenderConversationCompactionInstruction("", CompactionModeNormal, "")
	if normal != base {
		t.Fatalf("normal instruction = %q, want %q", normal, base)
	}
	if strings.Contains(normal, "immutable segments") || strings.Contains(normal, "## Corrections") {
		t.Fatalf("normal instruction leaked append guidance: %q", normal)
	}

	emergency := RenderConversationCompactionInstruction("", CompactionModeEmergency, "")
	if want := base + "\n\n" + compactionPromptEmergencyInstruction(); emergency != want {
		t.Fatalf("emergency instruction = %q, want %q", emergency, want)
	}

	if got, want := RenderConversationCompactionInstruction("custom compaction prompt", CompactionModeNormal, ""), "custom compaction prompt"; got != want {
		t.Fatalf("override normal instruction = %q, want %q", got, want)
	}
}
