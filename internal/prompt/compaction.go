package prompt

import (
	"github.com/luispabon/steiner/internal/provider"
)

const (
	compactionPromptSystemInstruction    = "You compress conversation history for the next model call."
	compactionPromptEmergencyInstruction = "This is an emergency handoff. Be shorter and more lossy than usual while preserving the essential task, the current state, and any irreversible decisions."
	compactionPromptInstructionBody      = `You are compacting the current working context for a coding agent.

The conversation history will be replaced by your summary. Another agent instance must be able to resume the task from your summary alone, without repeating investigation, losing important decisions, or corrupting unfinished work.

Write a high-quality handoff summary. Prioritize completeness, accuracy, and usefulness over brevity. Keep it structured and readable, but do not omit important facts just to make it short.

Include these sections:

## 1. Task and Goal

- The user's original request.
- The intended end state.
- Success criteria.
- Any explicit constraints, non-goals, or preferences from the user.
- Any assumptions currently being used.

## 2. Current Repository / Project State

- Relevant repo, branch, working directory, and environment details.
- Files inspected, created, modified, or deleted, with exact paths.
- Important existing code structure discovered.
- Any uncommitted changes, partially applied edits, generated artifacts, or dirty working tree state.
- Any external services, APIs, configs, or credentials assumed or referenced, without exposing secrets.

## 3. Work Completed

- What has already been implemented or changed.
- Commands run, with relevant outputs.
- Tests, linters, builds, or checks already executed.
- Results of those checks.
- Any commits, branches, PRs, or tickets created.

## 4. Key Findings and Decisions

- Important technical discoveries.
- Design decisions made and why.
- Tradeoffs considered.
- Constraints uncovered in the codebase or tooling.
- Dependencies, versions, config details, or integration behaviour that matter.
- User decisions or clarifications that must not be re-asked.

## 5. Problems Encountered

- Errors, failures, or confusing behaviour observed.
- Root causes if known.
- Fixes already applied.
- Workarounds used.
- Approaches tried and rejected, with reasons.
- Anything that looked promising but turned out to be wrong.

## 6. Remaining Work

- Specific next actions in priority order.
- Files or functions likely needing changes.
- Tests/checks still needed.
- Known blockers.
- Open questions that genuinely require user input.
- Risks or areas needing extra care.

## 7. Verification and Acceptance

- Exact commands/checks needed to prove completion.
- Expected successful outcomes.
- Manual checks, if automated verification is insufficient.
- Any known limitations in the verification already performed.

## 8. User Preferences and Interaction Context

- Relevant style, tone, formatting, or workflow preferences.
- Any project-specific conventions the user expects.
- Any promises made to the user.
- Anything the next agent should avoid repeating.

Rules:

- Be factual and specific.
- Preserve exact paths, filenames, symbols, commands, error messages, config keys, API names, versions, and decisions.
- Prefer concrete detail over vague summaries.
- Do not include irrelevant chat, pleasantries, or speculation.
- Do not expose secrets. If a secret appeared, refer to it only by purpose.
- Do not include hidden reasoning or private chain-of-thought. Summarize conclusions and rationale only.
- Do not claim something is complete unless it was implemented and verified.
- If work is partially complete, describe the exact safest continuation point.
- If something is uncertain, say exactly what is uncertain and why.
- Write for immediate continuation by a coding agent.`
)

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
		content = caveHumanCompactionVoice
	}
	if override != "" {
		content = override
	}
	if mode == CompactionModeEmergency {
		content = content + "\n\n" + compactionPromptEmergencyInstruction
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
	return compactionPromptSystemInstruction + "\n\n" + compactionPromptInstructionBody
}
