package prompt

import "strings"

const (
	templateCompactionSystem             = "compaction_system.md.tmpl"
	templateCompactionEmergency          = "compaction_emergency.md.tmpl"
	templateCompactionDefaultBody        = "compaction_default_body.md.tmpl"
	templateCompactionSegmentAppendIntro = "compaction_segment_append_intro.md.tmpl"
	templateCompactionSegmentAppendRules = "compaction_segment_append_rules.md.tmpl"
	templateCompactionRetry              = "compaction_retry.md.tmpl"
)

func compactionPromptSystemInstruction() string {
	return strings.TrimSpace(renderTemplate(templateCompactionSystem, nil))
}

func compactionPromptEmergencyInstruction() string {
	return strings.TrimSpace(renderTemplate(templateCompactionEmergency, nil))
}

func compactionPromptInstructionBody() string {
	return strings.TrimSpace(renderTemplate(templateCompactionDefaultBody, nil))
}

// segmentAppendIntro renders the segment append template's opening generic
// guidance, which a compaction override replaces.
func segmentAppendIntro() string {
	return strings.TrimSpace(renderTemplate(templateCompactionSegmentAppendIntro, nil))
}

// segmentAppendRules renders the segment append rules, which always stay in the
// instruction: a custom prompt cannot make segments mutable.
func segmentAppendRules() string {
	return strings.TrimSpace(renderTemplate(templateCompactionSegmentAppendRules, nil))
}

// CompactionMode selects the summarization style for a compaction request.
type CompactionMode string

const (
	// CompactionModeNormal asks for a full-fidelity handoff summary.
	CompactionModeNormal CompactionMode = "normal"
	// CompactionModeEmergency asks for a shorter, lossier handoff summary.
	CompactionModeEmergency CompactionMode = "emergency"
)

// RenderConversationCompactionInstruction renders the final instruction used to
// ask a model to compact the already-assembled conversation context.
func RenderConversationCompactionInstruction(override string, mode CompactionMode, steering string) string {
	base := compactionPromptSystem()
	if override != "" {
		base = override
	}
	content := applyCompactionSteering(base, steering)
	if mode == CompactionModeEmergency {
		content += "\n\n" + compactionPromptEmergencyInstruction()
	}
	return content
}

// RenderSegmentAppendInstruction renders the final instruction used to ask a
// model to summarize only the span after the last immutable segment. A
// compaction override replaces the template's opening generic guidance; the
// append rules always follow it, so a custom prompt cannot make segments
// mutable. Steering follows the rules, as in the conversation instruction.
func RenderSegmentAppendInstruction(override, steering string) string {
	intro := segmentAppendIntro()
	if override != "" {
		intro = override
	}
	return applyCompactionSteering(intro+"\n\n"+segmentAppendRules(), steering)
}

// applyCompactionSteering appends user steering after a rendered compaction
// instruction.
func applyCompactionSteering(base, steering string) string {
	if steering == "" {
		return base
	}
	return base + "\n\nAdditional user steering for this compaction:\n\n" + steering
}

func compactionPromptSystem() string {
	return compactionPromptSystemInstruction() + "\n\n" + compactionPromptInstructionBody()
}

// CompactionRetry describes a tail-only retry of a summary request whose previous
// reply carried no text.
type CompactionRetry struct {
	// ToolCalls reports that the previous reply carried tool calls and no text,
	// rather than no text alone.
	ToolCalls bool
	// Final marks the last attempt, which also anchors the reply format.
	Final bool
	// Attempted quotes the previous reply's tool calls, already capped by the caller.
	Attempted []string
	// Condense asks for a shorter complete rewrite of a reply cut off at the output limit.
	Condense bool
	// TargetWords is the approximate word budget for a condense rewrite; 0 omits it.
	TargetWords int
	// Draft is the truncated reply, already tag-escaped by the caller.
	Draft string
}

// RenderCompactionRetryInstruction renders the steering text a retry appends to
// the final compaction instruction. The output depends only on retry.
func RenderCompactionRetryInstruction(retry CompactionRetry) string {
	return strings.TrimSpace(renderTemplate(templateCompactionRetry, retry))
}
