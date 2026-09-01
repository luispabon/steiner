package prompt

import (
	"strings"
)

const identity = "You are steiner, a lean coding agent."

const (
	templateDelegation     = "delegation.md.tmpl"
	templateAdvisor        = "advisor.md.tmpl"
	templateCoreRules      = "core_rules.md.tmpl"
	templateToolBatching   = "tool_batching.md.tmpl"
	templateWorkflow       = "workflow_approval.md.tmpl"
	templateSandbox        = "sandbox.md.tmpl"
	templateExecutionModes = "execution_modes.md.tmpl"
)

type workflowMode string

// WorkflowMode selects which shared workflow instructions the system preamble renders.
type WorkflowMode = workflowMode

const (
	workflowModeParent         workflowMode = "parent"
	workflowModeDelegatedChild workflowMode = "delegated_child"
)

type sectionID string

const (
	sectionIdentity       sectionID = "identity"
	sectionSandbox        sectionID = "sandbox"
	sectionDelegation     sectionID = "delegation"
	sectionAdvisor        sectionID = "advisor"
	sectionCoreRules      sectionID = "core_rules"
	sectionToolBatching   sectionID = "tool_batching"
	sectionWorkflow       sectionID = "workflow"
	sectionExecutionModes sectionID = "execution_modes"
	sectionCaveHuman      sectionID = "cave_human"
)

type sectionContext struct {
	delegationEnabled     bool
	sandboxEnabled        bool
	sandboxWritableMounts []string
	advisorEnabled        bool
	caveHuman             bool
	workflowMode          workflowMode
}

type sectionRenderer func(sectionContext) string

var defaultSectionOrder = []sectionID{
	sectionIdentity,
	sectionDelegation,
	sectionAdvisor,
	sectionCoreRules,
	sectionToolBatching,
	sectionWorkflow,
	sectionSandbox,
	sectionExecutionModes,
	sectionCaveHuman,
}

var systemSections = map[sectionID]sectionRenderer{
	sectionIdentity: func(sectionContext) string {
		return identity
	},
	sectionSandbox: func(ctx sectionContext) string {
		if !ctx.sandboxEnabled {
			return ""
		}
		return renderSandboxInstruction(ctx.sandboxWritableMounts)
	},
	sectionDelegation: func(ctx sectionContext) string {
		if !ctx.delegationEnabled {
			return ""
		}
		return delegationInstructions(ctx.advisorEnabled)
	},
	sectionAdvisor: func(ctx sectionContext) string {
		if !ctx.advisorEnabled {
			return ""
		}
		return renderTemplate(templateAdvisor, nil)
	},
	sectionCoreRules: func(sectionContext) string {
		return renderTemplate(templateCoreRules, nil)
	},
	sectionToolBatching: func(sectionContext) string {
		return renderTemplate(templateToolBatching, nil)
	},
	sectionWorkflow: func(ctx sectionContext) string {
		return renderWorkflowInstructions(ctx.workflowMode)
	},
	sectionExecutionModes: func(ctx sectionContext) string {
		if ctx.workflowMode == workflowModeDelegatedChild {
			return ""
		}
		return renderTemplate(templateExecutionModes, nil)
	},
	sectionCaveHuman: func(ctx sectionContext) string {
		if !ctx.caveHuman {
			return ""
		}
		return caveHumanInstruction()
	},
}

// overrideSectionOrder is the section sequence used when the user supplies a
// system-prompt override: the shared rules, workflow, sandbox, and execution
// mode sections are replaced by the override text, but identity, the
// orchestration canon, and advisor guidance still render around it.
var overrideSectionOrder = []sectionID{
	sectionIdentity,
	sectionDelegation,
	sectionAdvisor,
	sectionToolBatching,
}

// delegationInstructions renders the orchestration canon: role prose, the
// specialist roster table, and the workflow/routing sections, from
// templates/delegation.md.tmpl. The `## Your workflow` advisor step renders
// only when advisorEnabled, so the canon is byte-identical within a session —
// advisorEnabled is session-fixed and part of the preamble cache key.
func delegationInstructions(advisorEnabled bool) string {
	return renderTemplate(templateDelegation, struct {
		Specialists    []specialistView
		AdvisorEnabled bool
	}{
		Specialists:    specialistViews(),
		AdvisorEnabled: advisorEnabled,
	})
}

// renderSandboxInstruction renders the sandbox section, listing the host paths
// mounted writable. With no mounts the section names only the current workdir.
func renderSandboxInstruction(mounts []string) string {
	return renderTemplate(templateSandbox, struct{ Mounts []string }{Mounts: mounts})
}

// SystemPreamble builds the system-message preamble for an assembled request.
func SystemPreamble(override string, delegationEnabled bool, caveHuman bool, systemSuffix string) ContextBlock {
	return SystemPreambleWithAdvisor(SystemPreambleParams{
		Override:          override,
		DelegationEnabled: delegationEnabled,
		Mode:              workflowModeParent,
		CaveHuman:         caveHuman,
		SystemSuffix:      systemSuffix,
	})
}

// SystemPreambleParams holds the inputs used to build the system preamble.
type SystemPreambleParams struct {
	Override              string
	DelegationEnabled     bool
	SandboxEnabled        bool
	SandboxWritableMounts []string
	AdvisorEnabled        bool
	Mode                  WorkflowMode
	CaveHuman             bool
	SystemSuffix          string
}

// SystemPreambleWithAdvisor builds the system-message preamble with optional advisor guidance.
func SystemPreambleWithAdvisor(params SystemPreambleParams) ContextBlock {
	return systemPreambleWithAdvisor(params)
}

func systemPreambleWithAdvisor(params SystemPreambleParams) ContextBlock {
	ctx := sectionContext{
		delegationEnabled:     params.DelegationEnabled,
		sandboxEnabled:        params.SandboxEnabled,
		sandboxWritableMounts: params.SandboxWritableMounts,
		advisorEnabled:        params.AdvisorEnabled,
		caveHuman:             params.CaveHuman,
		workflowMode:          normalizeWorkflowMode(params.Mode),
	}

	content := buildSystemPreamble(ctx)
	if params.Override != "" {
		content = buildOverridePreamble(strings.TrimSpace(params.Override), ctx)
	}

	if params.SystemSuffix != "" {
		content = content + "\n\n" + params.SystemSuffix
	}

	return ContextBlock{
		Source:   ContextSourcePreamble,
		Content:  content,
		ByteSize: len(content),
	}
}

func buildOverridePreamble(override string, ctx sectionContext) string {
	sections := renderSections(overrideSectionOrder, ctx)
	sections = append(sections, override)
	if caveHuman := strings.TrimSpace(systemSections[sectionCaveHuman](ctx)); caveHuman != "" {
		sections = append(sections, caveHuman)
	}
	return strings.Join(sections, "\n\n")
}

func buildSystemPreamble(ctx sectionContext) string {
	return strings.Join(renderSections(defaultSectionOrder, ctx), "\n\n")
}

// renderSections renders the given sections in order, dropping the ones that
// render empty. Each section is trimmed before joining so a template's
// leading or trailing blank line cannot alter inter-section spacing.
func renderSections(order []sectionID, ctx sectionContext) []string {
	sections := make([]string, 0, len(order))
	for _, id := range order {
		render, ok := systemSections[id]
		if !ok {
			continue
		}
		content := strings.TrimSpace(render(ctx))
		if content == "" {
			continue
		}
		sections = append(sections, content)
	}
	return sections
}

func renderWorkflowInstructions(mode workflowMode) string {
	return renderTemplate(templateWorkflow, struct{ DelegatedChild bool }{
		DelegatedChild: normalizeWorkflowMode(mode) == workflowModeDelegatedChild,
	})
}

func normalizeWorkflowMode(mode workflowMode) workflowMode {
	switch mode {
	case workflowModeDelegatedChild:
		return workflowModeDelegatedChild
	default:
		return workflowModeParent
	}
}

// ParentWorkflowMode returns the default workflow wording for parent runs.
func ParentWorkflowMode() workflowMode {
	return workflowModeParent
}

// DelegatedChildWorkflowMode returns workflow wording for delegated child runs.
func DelegatedChildWorkflowMode() workflowMode {
	return workflowModeDelegatedChild
}
