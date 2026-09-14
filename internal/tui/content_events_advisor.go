package tui

import (
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

func (b *contentBuffer) handleAdvisorStarted(event output.Event) {
	payload, ok := event.Payload.(output.AdvisorStartedEvent)
	if !ok {
		b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentStatus)
		return
	}

	agentID := event.Scope.AgentID
	if agentID != "" && b.appendScopedAdvisorStarted(agentID, payload) {
		return
	}

	idx := len(b.segments)
	modelName, reasoning := b.resolveDelegationModel(payload.Model)
	b.segments = append(b.segments, contentSegment{
		kind: segmentDelegation,
		delegData: &delegationDisplayState{
			isAdvisor:        true,
			toolLabel:        "advisor",
			taskPreview:      "stronger-model steering",
			currentOperation: "consulting stronger-model advisor",
			status:           "active",
			collapsed:        true,
			modelName:        modelName,
			reasoning:        reasoning,
			advisorUse:       payload.UseNumber,
			advisorMaxUses:   payload.MaxUses,
			advisorQuestion:  payload.Question,
			advisorFiles:     payload.Files,
			startTime:        nanoNow(),
		},
		renderDirty: true,
	})
	b.activeAdvisorSegment = idx + 1
}

func (b *contentBuffer) appendScopedAdvisorStarted(agentID string, payload output.AdvisorStartedEvent) bool {
	loc, active := b.activeDelegations[agentID]
	if !active || loc.dd == nil {
		return false
	}
	dd := loc.dd
	dd.advisorBudget = payload.MaxUses
	dd.advisorUses = payload.UseNumber
	dd.advisorQuestion = payload.Question
	dd.advisorFiles = payload.Files
	b.markDelegationDirty(loc.seg)
	return true
}

func (b *contentBuffer) handleAdvisorComplete(event output.Event) {
	payload, ok := event.Payload.(output.AdvisorCompleteEvent)
	if !ok {
		b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentStatus)
		return
	}

	agentID := event.Scope.AgentID
	if agentID != "" && b.appendScopedAdvisorComplete(agentID, payload) {
		return
	}

	idx := b.activeAdvisorSegment - 1
	if idx < 0 || idx >= len(b.segments) || b.segments[idx].kind != segmentDelegation || b.segments[idx].delegData == nil || !b.segments[idx].delegData.isAdvisor {
		idx = len(b.segments)
		b.segments = append(b.segments, contentSegment{
			kind:        segmentDelegation,
			delegData:   &delegationDisplayState{isAdvisor: true, toolLabel: "advisor", collapsed: true},
			renderDirty: true,
		})
	}
	dd := b.segments[idx].delegData
	dd.modelName, dd.reasoning = b.resolveDelegationModel(payload.Model)
	dd.advisorUse = payload.UseNumber
	dd.advisorMaxUses = payload.MaxUses
	dd.elapsed = formatElapsed(dd.startTime, nanoNow())
	dd.output = payload.Note
	dd.status = "complete"
	dd.resultStatus = "complete"
	dd.applyUsage(payload.CacheReadTokens, payload.InputTokens, payload.CacheCreateTokens, payload.TokenCount)
	if strings.TrimSpace(payload.Error) != "" {
		dd.output = payload.Error
		dd.status = "failed"
		dd.resultStatus = "failed"
	}
	b.segments[idx].renderDirty = true
	b.gen++
	b.activeAdvisorSegment = 0

	// Append labeled block with advisor note outside the box.
	if body := strings.TrimSpace(dd.output); body != "" {
		b.appendLabeledBlock("Advisor output", body)
	}

	// Trailing blank margin after the closing separator.
	b.segments = append(b.segments, contentSegment{
		kind:        segmentPlain,
		text:        " ",
		renderDirty: true,
	})
}

func (b *contentBuffer) appendScopedAdvisorComplete(agentID string, payload output.AdvisorCompleteEvent) bool {
	loc, active := b.activeDelegations[agentID]
	if !active || loc.dd == nil {
		return false
	}
	dd := loc.dd
	dd.advisorUses = payload.UseNumber
	dd.advisorBudget = payload.MaxUses
	b.markDelegationDirty(loc.seg)
	return true
}

func (b *contentBuffer) handleAdvisorBudgetExhausted(event output.Event) {
	payload, ok := event.Payload.(output.AdvisorBudgetExhaustedEvent)
	if !ok {
		b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentStatus)
		return
	}

	agentID := event.Scope.AgentID
	if agentID != "" && b.appendScopedAdvisorBudgetExhausted(agentID, payload) {
		return
	}

	modelName, reasoning := b.resolveDelegationModel(payload.Model)
	b.segments = append(b.segments, contentSegment{
		kind: segmentDelegation,
		delegData: &delegationDisplayState{
			isAdvisor:       true,
			toolLabel:       "advisor",
			taskPreview:     "stronger-model steering",
			status:          "budget_exhausted",
			resultStatus:    "budget exhausted",
			output:          payload.Message,
			collapsed:       true,
			modelName:       modelName,
			reasoning:       reasoning,
			advisorUse:      payload.Used,
			advisorMaxUses:  payload.MaxUses,
			advisorQuestion: payload.Question,
			advisorFiles:    payload.Files,
		},
		renderDirty: true,
	})
}

func (b *contentBuffer) appendScopedAdvisorBudgetExhausted(agentID string, payload output.AdvisorBudgetExhaustedEvent) bool {
	loc, active := b.activeDelegations[agentID]
	if !active || loc.dd == nil {
		return false
	}
	dd := loc.dd
	dd.advisorBudget = payload.MaxUses
	dd.advisorUses = payload.Used
	dd.advisorDenied++
	b.markDelegationDirty(loc.seg)
	return true
}

func (b *contentBuffer) handleAdvisorThinkingChunk(event output.Event) {
	idx := b.activeAdvisorSegment - 1
	if idx < 0 || idx >= len(b.segments) || b.segments[idx].kind != segmentDelegation || b.segments[idx].delegData == nil || !b.segments[idx].delegData.isAdvisor {
		return
	}
	dd := b.segments[idx].delegData
	if b.applyDelegationThinkingChunk(dd, event) {
		b.segments[idx].renderDirty = true
		b.gen++
	}
}
