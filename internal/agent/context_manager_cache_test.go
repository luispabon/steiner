package agent

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
)

func TestCachedSystemPreambleCacheAndInvalidation(t *testing.T) {
	manager := &baseContextManager{}
	base := func() string {
		return manager.CachedSystemPreamble("override", false, false, config.OrchestrationLevel(""), false, false, prompt.WorkflowMode(""), false, "suffix", false, nil)
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
			return manager.CachedSystemPreamble("override", false, false, config.OrchestrationLevel(""), false, false, prompt.WorkflowMode(""), true, "suffix", false, nil)
		}},
		{name: "override", change: func() string {
			return manager.CachedSystemPreamble("changed", false, false, config.OrchestrationLevel(""), false, false, prompt.WorkflowMode(""), false, "suffix", false, nil)
		}},
		{name: "system suffix", change: func() string {
			return manager.CachedSystemPreamble("override", false, false, config.OrchestrationLevel(""), false, false, prompt.WorkflowMode(""), false, "changed", false, nil)
		}},
		{name: "lsp enabled", change: func() string {
			return manager.CachedSystemPreamble("override", false, false, config.OrchestrationLevel(""), false, true, prompt.WorkflowMode(""), false, "suffix", false, nil)
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

func TestCachedSystemPreambleMaterialInputsInvalidateCache(t *testing.T) {
	cases := []struct {
		name     string
		baseline func(*baseContextManager) string
		change   func(*baseContextManager) string
	}{
		{name: "delegation enabled", change: func(manager *baseContextManager) string {
			return manager.CachedSystemPreamble("", true, false, config.OrchestrationLevelStandard, false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		}},
		{name: "async sub-agents", baseline: func(manager *baseContextManager) string {
			return manager.CachedSystemPreamble("", true, false, config.OrchestrationLevelStandard, false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		}, change: func(manager *baseContextManager) string {
			return manager.CachedSystemPreamble("", true, true, config.OrchestrationLevelStandard, false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		}},
		{name: "advisor enabled", change: func(manager *baseContextManager) string {
			return manager.CachedSystemPreamble("", false, false, config.OrchestrationLevel(""), true, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		}},
		{name: "orchestration level", baseline: func(manager *baseContextManager) string {
			return manager.CachedSystemPreamble("", true, false, config.OrchestrationLevelStandard, false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		}, change: func(manager *baseContextManager) string {
			return manager.CachedSystemPreamble("", true, false, config.OrchestrationLevelLow, false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
		}},
		{name: "workflow mode", change: func(manager *baseContextManager) string {
			return manager.CachedSystemPreamble("", false, false, config.OrchestrationLevel(""), false, false, prompt.DelegatedChildWorkflowMode(), false, "", false, nil)
		}},
		{name: "sandbox enabled", change: func(manager *baseContextManager) string {
			return manager.CachedSystemPreamble("", false, false, config.OrchestrationLevel(""), false, false, prompt.ParentWorkflowMode(), false, "", true, nil)
		}},
		{name: "sandbox writable mounts", change: func(manager *baseContextManager) string {
			return manager.CachedSystemPreamble("", false, false, config.OrchestrationLevel(""), false, false, prompt.ParentWorkflowMode(), false, "", true, []string{"/tmp"})
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			manager := &baseContextManager{}
			first := manager.CachedSystemPreamble("", false, false, config.OrchestrationLevel(""), false, false, prompt.ParentWorkflowMode(), false, "", false, nil)
			if tt.baseline != nil {
				first = tt.baseline(manager)
			}
			if got := tt.change(manager); got == first {
				t.Fatal("changed input did not invalidate preamble")
			}
		})
	}
}

func TestCachedSystemPreambleCopiesSandboxMounts(t *testing.T) {
	manager := &baseContextManager{}
	mounts := []string{"/var/log"}
	first := manager.CachedSystemPreamble("", false, false, config.OrchestrationLevel(""), false, false, prompt.ParentWorkflowMode(), false, "", true, mounts)
	mounts[0] = "/tmp"
	mounts = append(mounts, "/home/u/go")
	unchanged := manager.CachedSystemPreamble("", false, false, config.OrchestrationLevel(""), false, false, prompt.ParentWorkflowMode(), false, "", true, []string{"/var/log"})
	if unchanged != first {
		t.Fatal("cached preamble changed after caller mutated mounts")
	}
	regenerated := manager.CachedSystemPreamble("", false, false, config.OrchestrationLevel(""), false, false, prompt.ParentWorkflowMode(), false, "", true, mounts)
	if regenerated == first || !strings.Contains(regenerated, "Additional writable paths: /tmp, /home/u/go") {
		t.Fatalf("regenerated preamble = %q, want mutated mounts", regenerated)
	}
}
