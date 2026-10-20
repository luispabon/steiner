package output

import "strings"

// AdvisorStartedEvent records a stronger-model advisor call beginning.
type AdvisorStartedEvent struct {
	Model     string   `json:"model,omitempty"`
	UseNumber int      `json:"use_number"`
	MaxUses   int      `json:"max_uses"`
	Question  string   `json:"question,omitempty"`
	Files     []string `json:"files,omitempty"`
}

// AdvisorCompleteEvent records a completed advisor call.
type AdvisorCompleteEvent struct {
	Model             string `json:"model,omitempty"`
	UseNumber         int    `json:"use_number"`
	MaxUses           int    `json:"max_uses"`
	Note              string `json:"note,omitempty"`
	Error             string `json:"error,omitempty"`
	Truncated         bool   `json:"truncated,omitempty"`
	CacheReadTokens   int    `json:"cache_read_tokens,omitempty"`
	CacheCreateTokens int    `json:"cache_create_tokens,omitempty"`
	InputTokens       int    `json:"input_tokens,omitempty"`
	TokenCount        int    `json:"token_count,omitempty"`
}

// AdvisorBudgetExhaustedEvent records a skipped advisor call after the per-run
// budget was exhausted.
type AdvisorBudgetExhaustedEvent struct {
	Model    string   `json:"model,omitempty"`
	Used     int      `json:"used"`
	MaxUses  int      `json:"max_uses"`
	Message  string   `json:"message,omitempty"`
	Question string   `json:"question,omitempty"`
	Files    []string `json:"files,omitempty"`
}

// NewAdvisorStartedEvent creates an advisor_started event.
func NewAdvisorStartedEvent(model string, useNumber, maxUses int, question string, files []string) Event {
	return newEvent(EventTypeAdvisorStarted, AdvisorStartedEvent{
		Model:     strings.TrimSpace(model),
		UseNumber: useNumber,
		MaxUses:   maxUses,
		Question:  strings.TrimSpace(question),
		Files:     append([]string(nil), files...),
	})
}

// AdvisorCompleteParams holds the arguments for NewAdvisorCompleteEvent.
type AdvisorCompleteParams struct {
	Model             string
	UseNumber         int
	MaxUses           int
	Note              string
	Truncated         bool
	Err               error
	CacheReadTokens   int
	CacheCreateTokens int
	InputTokens       int
	TokenCount        int
}

// NewAdvisorCompleteEvent creates an advisor_complete event.
func NewAdvisorCompleteEvent(p AdvisorCompleteParams) Event {
	payload := AdvisorCompleteEvent{
		Model:             strings.TrimSpace(p.Model),
		UseNumber:         p.UseNumber,
		MaxUses:           p.MaxUses,
		Note:              strings.TrimSpace(p.Note),
		Truncated:         p.Truncated,
		CacheReadTokens:   p.CacheReadTokens,
		CacheCreateTokens: p.CacheCreateTokens,
		InputTokens:       p.InputTokens,
		TokenCount:        p.TokenCount,
	}
	if p.Err != nil {
		payload.Error = p.Err.Error()
	}
	return newEvent(EventTypeAdvisorComplete, payload)
}

// NewAdvisorBudgetExhaustedEvent creates an advisor_budget_exhausted event.
func NewAdvisorBudgetExhaustedEvent(model string, used, maxUses int, message, question string, files []string) Event {
	return newEvent(EventTypeAdvisorBudgetExhausted, AdvisorBudgetExhaustedEvent{
		Model:    strings.TrimSpace(model),
		Used:     used,
		MaxUses:  maxUses,
		Message:  strings.TrimSpace(message),
		Question: strings.TrimSpace(question),
		Files:    append([]string(nil), files...),
	})
}
