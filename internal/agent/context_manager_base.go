package agent

import (
	"slices"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/tool"
)

type baseContextManager struct {
	fileTracker           FileTracker
	readAnnotations       bool
	annotationsConfigured bool
	cachedPreamble        struct {
		content               string
		override              string
		delegationEnabled     bool
		asyncSubAgents        bool
		orchestrationLevel    config.OrchestrationLevel
		advisorEnabled        bool
		lspEnabled            bool
		workflowMode          prompt.WorkflowMode
		caveHuman             bool
		systemSuffix          string
		sandboxEnabled        bool
		sandboxWritableMounts []string
	}
	events output.EventSink
}

// CachedSystemPreamble returns the memoized system preamble for the given
// inputs, rebuilding it only when one of them changes since the last call.
// This keeps the preamble byte-identical across turns within a session,
// which is required for prompt-cache reuse; orchestrationLevel changing
// (e.g. via a mid-session /orchestration switch) is one of the inputs that
// invalidates the cache like any other.
func (b *baseContextManager) CachedSystemPreamble(override string, delegationEnabled bool, asyncSubAgents bool, orchestrationLevel config.OrchestrationLevel, advisorEnabled bool, lspEnabled bool, workflowMode prompt.WorkflowMode, caveHuman bool, systemSuffix string, sandboxEnabled bool, sandboxWritableMounts []string) string {
	if b.cachedPreamble.content == "" ||
		b.cachedPreamble.override != override ||
		b.cachedPreamble.delegationEnabled != delegationEnabled ||
		b.cachedPreamble.asyncSubAgents != asyncSubAgents ||
		b.cachedPreamble.orchestrationLevel != orchestrationLevel ||
		b.cachedPreamble.advisorEnabled != advisorEnabled ||
		b.cachedPreamble.lspEnabled != lspEnabled ||
		b.cachedPreamble.workflowMode != workflowMode ||
		b.cachedPreamble.caveHuman != caveHuman ||
		b.cachedPreamble.systemSuffix != systemSuffix ||
		b.cachedPreamble.sandboxEnabled != sandboxEnabled ||
		!slices.Equal(b.cachedPreamble.sandboxWritableMounts, sandboxWritableMounts) {
		b.cachedPreamble.content = prompt.SystemPreambleWithAdvisor(prompt.SystemPreambleParams{
			Override:              override,
			DelegationEnabled:     delegationEnabled,
			AsyncSubAgents:        asyncSubAgents,
			OrchestrationLevel:    orchestrationLevel,
			AdvisorEnabled:        advisorEnabled,
			LSPEnabled:            lspEnabled,
			Mode:                  workflowMode,
			CaveHuman:             caveHuman,
			SystemSuffix:          systemSuffix,
			SandboxEnabled:        sandboxEnabled,
			SandboxWritableMounts: sandboxWritableMounts,
		}).Content
		b.cachedPreamble.override = override
		b.cachedPreamble.delegationEnabled = delegationEnabled
		b.cachedPreamble.asyncSubAgents = asyncSubAgents
		b.cachedPreamble.orchestrationLevel = orchestrationLevel
		b.cachedPreamble.advisorEnabled = advisorEnabled
		b.cachedPreamble.lspEnabled = lspEnabled
		b.cachedPreamble.workflowMode = workflowMode
		b.cachedPreamble.caveHuman = caveHuman
		b.cachedPreamble.systemSuffix = systemSuffix
		b.cachedPreamble.sandboxEnabled = sandboxEnabled
		b.cachedPreamble.sandboxWritableMounts = append([]string(nil), sandboxWritableMounts...)
	}
	return b.cachedPreamble.content
}

func (b *baseContextManager) RecordMutation(path string) {
	b.fileTracker.RecordMutation(path)
}

// FileObserved reports whether path was read this session, backing the
// mutate replace-operation observation guard (see tool.FileObservedChecker).
func (b *baseContextManager) FileObserved(path string) bool {
	return b.fileTracker.WasObserved(path)
}

// FileReadState returns the tracker's record of its last read of path as seen
// at turn, backing mutate's failure diagnostics (see tool.FileReadState).
func (b *baseContextManager) FileReadState(path string, turn int) tool.FileReadState {
	return b.fileTracker.ReadState(path, turn)
}

func (b *baseContextManager) SetEventSink(sink output.EventSink) {
	b.events = sink
}

func (b *baseContextManager) observeToolResult(_ int, toolName string, _ map[string]any, content string) string {
	return tool.ShapeIngestedToolResult(toolName, content)
}
