package prompt

import "strings"

const (
	templateCompactionSystem        = "compaction_system.md.tmpl"
	templateCompactionEmergency     = "compaction_emergency.md.tmpl"
	templateCompactionDefaultBody   = "compaction_default_body.md.tmpl"
	templateCompactionSegmentAppend = "compaction_segment_append.md.tmpl"
	templateCompactionRetry         = "compaction_retry.md.tmpl"
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

func compactionPromptSegmentAppendInstruction() string {
	return strings.TrimSpace(renderTemplate(templateCompactionSegmentAppend, nil))
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
func RenderConversationCompactionInstruction(override string, mode CompactionMode, steerings ...string) string {
	content := compactionInstruction(compactionPromptSystem(), override, firstSteering(steerings))
	if mode == CompactionModeEmergency {
		content += "\n\n" + compactionPromptEmergencyInstruction()
	}
	return content
}

// RenderSegmentAppendInstruction renders the final instruction used to ask a
// model to summarize only the span after the last immutable segment. The
// override and steering semantics match RenderConversationCompactionInstruction.
func RenderSegmentAppendInstruction(override string, steerings ...string) string {
	return compactionInstruction(compactionPromptSegmentAppendInstruction(), override, firstSteering(steerings))
}

// compactionInstruction applies a model compaction override and user steering
// to a base instruction. The override replaces the base; steering follows it.
func compactionInstruction(base, override, steering string) string {
	if override != "" {
		base = override
	}
	if steering != "" {
		base += "\n\nAdditional user steering for this compaction:\n\n" + steering
	}
	return base
}

func firstSteering(steerings []string) string {
	if len(steerings) == 0 {
		return ""
	}
	return steerings[0]
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
