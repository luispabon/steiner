package prompt

import "strings"

const (
	templateCompactionSystem      = "compaction_system.md.tmpl"
	templateCompactionEmergency   = "compaction_emergency.md.tmpl"
	templateCompactionDefaultBody = "compaction_default_body.md.tmpl"
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
func RenderConversationCompactionInstruction(override string, mode CompactionMode, caveHuman bool, steerings ...string) string {
	steering := ""
	if len(steerings) > 0 {
		steering = steerings[0]
	}
	content := compactionPromptSystem()
	if caveHuman {
		content = caveHumanCompactionVoice()
	}
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
