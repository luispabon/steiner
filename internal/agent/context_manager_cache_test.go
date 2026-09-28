package agent

import (
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
)

func TestCachedSystemPreambleCacheAndInvalidation(t *testing.T) {
	manager := &baseContextManager{}
	base := func() string {
		return manager.CachedSystemPreamble("override", false, config.OrchestrationLevel(""), false, false, prompt.WorkflowMode(""), false, "suffix", false, nil)
	}
	first := base()
	if got := base(); got != first {
		t.Fatalf("identical inputs returned different preambles")
	}
	cases := []struct {
		name   string
		change func() string
	}{
		{name: "cave human", change: func() string {
			return manager.CachedSystemPreamble("override", false, config.OrchestrationLevel(""), false, false, prompt.WorkflowMode(""), true, "suffix", false, nil)
		}},
		{name: "override", change: func() string {
			return manager.CachedSystemPreamble("changed", false, config.OrchestrationLevel(""), false, false, prompt.WorkflowMode(""), false, "suffix", false, nil)
		}},
		{name: "system suffix", change: func() string {
			return manager.CachedSystemPreamble("override", false, config.OrchestrationLevel(""), false, false, prompt.WorkflowMode(""), false, "changed", false, nil)
		}},
		{name: "lsp enabled", change: func() string {
			return manager.CachedSystemPreamble("override", false, config.OrchestrationLevel(""), false, true, prompt.WorkflowMode(""), false, "suffix", false, nil)
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.change()
			if got == first {
				t.Fatal("changed input did not invalidate preamble")
			}
		})
	}
}
