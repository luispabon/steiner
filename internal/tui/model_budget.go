package tui

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

func (m *Model) contextBudgetForModel(name string) int {
	if m == nil || len(m.modelContexts) == 0 {
		return 0
	}
	return m.modelContexts[strings.TrimSpace(name)]
}

func (m *Model) applyContextBudget(payload output.ContextBudgetEvent) {
	if m == nil {
		return
	}
	if payload.PromptTokens == 0 && payload.ContextWindow <= 0 && payload.ContextTokens <= 0 {
		if payload.Turn > 0 {
			m.sidebar.currentTurn = payload.Turn
		}
		return
	}

	contextWindow := payload.ContextWindow
	if contextWindow <= 0 {
		contextWindow = payload.ContextTokens
	}
	if contextWindow <= 0 {
		return
	}
	promptUsed := payload.RawPromptTokens
	if promptUsed <= 0 {
		promptUsed = payload.PromptTokens
	}
	if promptUsed < 0 {
		promptUsed = 0
	}
	budgetUsed := payload.TotalTokens
	if budgetUsed < 0 {
		budgetUsed = 0
	}
	if budgetUsed == 0 {
		budgetUsed = promptUsed
	}
	m.sidebar.promptUsed = promptUsed
	m.sidebar.budgetUsed = budgetUsed
	m.sidebar.contextBudget = contextWindow
	m.status.promptUsed = promptUsed
	m.status.contextBudget = contextWindow
	if payload.Turn > 0 {
		m.sidebar.currentTurn = payload.Turn
	}
	m.status.context = fmt.Sprintf("ctx %d/%d", promptUsed, contextWindow)
}
