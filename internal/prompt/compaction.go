package prompt

import "strings"

const (
	templateCompactionSystem      = "compaction_system.md.tmpl"
	templateCompactionEmergency   = "compaction_emergency.md.tmpl"
	templateCompactionDefaultBody = "compaction_default_body.md.tmpl"
	templateCompactionRetry       = "compaction_retry.md.tmpl"
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
	steering := ""
	if len(steerings) > 0 {
		steering = steerings[0]
	}
	content := compactionPromptSystem()
	if override != "" {
		content = override
	}
	if steering != "" {
		content += "\n\nAdditional user steering for this compaction:\n\n" + steering
	}
	if mode == CompactionModeEmergency {
		content = content + "\n\n" + compactionPromptEmergencyInstruction()
	}
	return content
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
}

// RenderCompactionRetryInstruction renders the steering text a retry appends to
// the final compaction instruction. The output depends only on retry.
func RenderCompactionRetryInstruction(retry CompactionRetry) string {
	return strings.TrimSpace(renderTemplate(templateCompactionRetry, retry))
}
