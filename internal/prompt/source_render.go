package prompt

import (
	"context"
	"path/filepath"

	"github.com/luispabon/steiner/internal/provider"
)

type assemblyState struct {
	pendingBlocks  []ContextBlock
	blocks         []ContextBlock
	messages       []provider.Message
	budgets        *budgetTracker
	renderedBlocks int
}

func newAssemblyState(policy AssemblyPolicy, opts AssemblyOptions) assemblyState {
	return assemblyState{
		pendingBlocks: make([]ContextBlock, 0, 8),
		blocks:        make([]ContextBlock, 0, 8),
		messages:      make([]provider.Message, 0, 8+len(opts.Conversation)+len(opts.ToolResults)),
		budgets:       newBudgetTracker(policy.Budgets),
	}
}

func (s *assemblyState) appendBlock(block ContextBlock) {
	s.pendingBlocks = append(s.pendingBlocks, block)
}

func (s *assemblyState) appendMessage(message provider.Message) {
	s.messages = append(s.messages, message)
}

func (s *assemblyState) renderBlocks() {
	for _, block := range s.pendingBlocks[s.renderedBlocks:] {
		clipped, truncated, ok := applyBudget(s.budgets, block.Source, block.Content)
		if !ok && len(block.Content) > 0 {
			continue
		}
		block.Content = clipped
		block.ByteSize = len(clipped)
		if truncated {
			block.Truncated = true
		}
		s.blocks = append(s.blocks, block)
		msg := blockMessage(block)
		if len(s.messages) > 0 && s.messages[len(s.messages)-1].Role == msg.Role {
			switch msg.Role {
			case provider.MessageRoleSystem:
				s.messages[len(s.messages)-1].Content += "\n\n" + msg.Content
			case provider.MessageRoleUser:
				s.messages[len(s.messages)-1].Content += "\n" + msg.Content
			default:
				s.messages = append(s.messages, msg)
			}
		} else {
			s.messages = append(s.messages, msg)
		}
	}
	s.renderedBlocks = len(s.pendingBlocks)
}

func (plan sourcePlan) render(ctx context.Context, policy AssemblyPolicy, opts AssemblyOptions) (Assembly, error) {
	state := newAssemblyState(policy, opts)

	for _, step := range plan.Steps {
		if err := step.Apply(ctx, &state); err != nil {
			return Assembly{}, err
		}
		state.renderBlocks()
	}

	return Assembly{
		Messages: state.messages,
		Blocks:   state.blocks,
	}, nil
}

func applyBudget(tracker *budgetTracker, source ContextSource, content string) (string, bool, bool) {
	// AGENTS.md is exempt from byte budgeting: full content is always delivered.
	switch source {
	case ContextSourceGlobalAgentsMD, ContextSourceProjectAgentsMD:
		return content, false, true
	}
	if content == "" {
		return "", false, true
	}
	allowed := tracker.take(source, len(content))
	if allowed <= 0 {
		return "", false, false
	}
	if allowed >= len(content) {
		return content, false, true
	}
	return truncateText(content, allowed), true, true
}

func blockMessage(block ContextBlock) provider.Message {
	message := provider.Message{
		Content: block.Content,
	}

	switch block.Source {
	case ContextSourcePreamble, ContextSourcePhasePrompt, ContextSourceGlobalAgentsMD, ContextSourceProjectAgentsMD:
		message.Role = provider.MessageRoleSystem
	case ContextSourceConversationSummary:
		message.Role = provider.MessageRoleSystem
	case ContextSourceToolSummary, ContextSourceToolResult, ContextSourceDelegationResult:
		message.Role = provider.MessageRoleTool
	default:
		// Durable context intentionally stays user-scoped here; the interactive
		// context report uses a different role mapping for block matching.
		message.Role = provider.MessageRoleUser
	}

	if block.Path != "" {
		message.Name = block.Path
	}
	if block.Source == ContextSourceSkill && block.Path != "" {
		message.Name = filepath.Base(block.Path)
	}

	return message
}
