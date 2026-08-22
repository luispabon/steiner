package prompt

import (
	"strings"

	"github.com/luispabon/steiner/internal/provider"
)

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
func RenderConversationCompactionInstruction(override string, mode CompactionMode, caveHuman bool) string {
	content := compactionPromptSystem()
	if caveHuman {
		content = caveHumanCompactionVoice()
	}
	if override != "" {
		content = override
	}
	if mode == CompactionModeEmergency {
		content = content + "\n\n" + compactionPromptEmergencyInstruction()
	}
	return content
}

// ToolSummaryEnvelope stores a summarized tool message in a bounded serialized form.
type ToolSummaryEnvelope struct {
	Kind      string               `json:"kind"`
	Name      string               `json:"name,omitempty"`
	Role      provider.MessageRole `json:"role,omitempty"`
	ByteSize  int                  `json:"byte_size"`
	Truncated bool                 `json:"truncated,omitempty"`
	Content   string               `json:"content"`
}

func summarizeToolMessage(message provider.Message, policy ToolSummaryPolicy) ContextBlock {
	limit := policy.MaxBytes
	if limit <= 0 {
		limit = defaultToolSummaryBudgetBytes
	}
	content := truncateText(message.Content, limit)
	envelope := ToolSummaryEnvelope{
		Kind:      "tool_summary",
		Name:      message.Name,
		Role:      provider.MessageRoleTool,
		ByteSize:  len(message.Content),
		Truncated: len(message.Content) > len(content),
		Content:   content,
	}
	encoded := marshalEnvelope(envelope)
	block := ContextBlock{
		Source:    ContextSourceToolSummary,
		Path:      message.Name,
		Content:   encoded,
		ByteSize:  len(encoded),
		Truncated: envelope.Truncated,
	}
	return block
}

func compactionPromptSystem() string {
	return compactionPromptSystemInstruction() + "\n\n" + compactionPromptInstructionBody()
}
