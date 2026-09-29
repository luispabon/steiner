package delegation

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// EventSinkScoper is implemented by providers whose emitted events flow to a
// fixed sink. WithEventSink returns a copy whose sink is wrapped by wrap; the
// receiver must not be mutated.
type EventSinkScoper interface {
	WithEventSink(wrap func(output.EventSink) output.EventSink) provider.Provider
}

// scopeProviderEvents returns a provider whose events carry the child agent
// scope, or p unchanged when it cannot scope its events.
func scopeProviderEvents(p provider.Provider, agentID string, agentType AgentType) provider.Provider {
	scoper, ok := p.(EventSinkScoper)
	if !ok || agentID == "" {
		return p
	}
	return scoper.WithEventSink(func(sink output.EventSink) output.EventSink {
		return withAgentScope(agentID, agentType, sink)
	})
}

// ensureSupervisor defaults the controller and supervisor when the caller did
// not inject runtime-scoped ones.
func ensureSupervisor(deps *SubAgentHandlerDeps) {
	if deps.ActiveController == nil {
		deps.ActiveController = NewActiveController()
	}
	if deps.Supervisor == nil {
		deps.Supervisor = NewSupervisor(SupervisorOptions{
			MaxParallel: max(deps.SubAgentCfg.MaxParallel, 1),
			Controller:  deps.ActiveController,
		})
	}
}

// delegatePlan is the per-child run state. A code agent's worktree, request,
// warnings and remediation only exist after provision has run at dequeue time.
type delegatePlan struct {
	req         agent.RunRequest
	worktree    CodeWorktree
	warnings    []string
	remediation *RemediationConfig
	limits      Limits
	modelAlias  string
	// provision, when set, fills the plan's worktree-dependent fields on the
	// supervisor goroutine before the child is registered.
	provision   func(ctx context.Context, plan *delegatePlan) error
	provisioned bool
}

// ready reports whether the plan's request and worktree are populated.
func (p *delegatePlan) ready() bool {
	return p.provision == nil || p.provisioned
}

// superviseDelegate runs a child through the supervisor and blocks for its
// result. prepare, when set, provisions the worktree at dequeue time.
// setupFailed runs when the child never started (rejected at enqueue or failed
// to prepare).
func superviseDelegate(
	ctx context.Context,
	deps SubAgentHandlerDeps,
	spec Spec,
	worktree CodeWorktree,
	prepare func(childCtx context.Context) (CodeWorktree, error),
	execute func(childCtx context.Context) (tool.ExecutionResult, error),
	cancelledBeforeStart func() tool.ExecutionResult,
	setupFailed func(),
) (tool.ExecutionResult, error) {
	var began atomic.Bool
	result, err := deps.Supervisor.SpawnAndWait(ctx, ChildJob{
		AgentID:          spec.AgentID,
		AgentType:        spec.AgentType,
		ParentCallID:     spec.ParentCallID,
		ObjectivePreview: truncateTaskPreview(spec.Task, 120),
		Worktree:         worktree,
		Prepare:          prepare,
		Execute: func(childCtx context.Context) (tool.ExecutionResult, error) {
			began.Store(true)
			return execute(childCtx)
		},
		OnCancelledBeforeStart: func() tool.ExecutionResult {
			began.Store(true)
			return cancelledBeforeStart()
		},
	})
	if err == nil {
		return result, nil
	}
	if began.Load() {
		return tool.ExecutionResult{}, err
	}
	if setupFailed != nil {
		setupFailed()
	}
	return tool.ExecutionResult{}, childSetupError(err)
}

func runRegisteredDelegate(
	ctx context.Context,
	deps SpecializedToolDeps,
	spec Spec,
	plan *delegatePlan,
	failureLabel string,
	decorate func(tool.ExecutionResult) tool.ExecutionResult,
) (tool.ExecutionResult, error) {
	sub := deps.SubAgentHandlerDeps
	var prepare func(context.Context) (CodeWorktree, error)
	if plan.provision != nil {
		prepare = func(childCtx context.Context) (CodeWorktree, error) {
			if err := plan.provision(childCtx, plan); err != nil {
				emitDelegateFailed(sub.Events, spec, spec.AgentType, err.Error())
				return CodeWorktree{}, err
			}
			plan.provisioned = true
			return plan.worktree, nil
		}
	}
	return superviseDelegate(ctx, sub, spec, plan.worktree, prepare,
		func(childCtx context.Context) (tool.ExecutionResult, error) {
			return executeRegisteredDelegate(childCtx, sub, spec, plan, failureLabel, decorate)
		},
		func() tool.ExecutionResult {
			emitDelegateStarted(sub.Events, spec, plan.modelAlias, spec.AgentType)
			return finishCancelledBeforeDispatch(sub, spec, plan, decorate)
		},
		func() {
			removeAndCloseToolCallTraceWriter(spec.AgentID)
			cleanupRegistrationWorktree(spec.AgentType, sub.WorkDir, plan.worktree)
		},
	)
}

// executeRegisteredDelegate performs all child work and finalisation on the
// supervisor-owned goroutine. Registration and completion are the supervisor's.
func executeRegisteredDelegate(
	childCtx context.Context,
	deps SubAgentHandlerDeps,
	spec Spec,
	plan *delegatePlan,
	failureLabel string,
	decorate func(tool.ExecutionResult) tool.ExecutionResult,
) (tool.ExecutionResult, error) {
	spec.Limits = plan.limits
	req, worktree, remediation := plan.req, plan.worktree, plan.remediation
	emitDelegateStarted(deps.Events, spec, plan.modelAlias, spec.AgentType)

	var gateRelease func()
	req.Events, gateRelease = applyDispatchGate(childCtx, deps.CacheKeyStore, req.PromptCacheKey, spec.AgentID, spec.ParentCallID, deps.Events, req.Events)
	defer gateRelease()
	if childCtx.Err() != nil {
		plan.req = req
		return finishCancelledBeforeDispatch(deps, spec, plan, decorate), nil
	}

	var opts []spawnOption
	if remediation != nil {
		opts = append(opts, WithRemediation(remediation))
	}
	result, state, runUsage, err := SpawnDelegate(childCtx, spec, req, deps.Runner, deps.Events, deps.TraceLogger, opts...)
	if err == nil && deps.SessionStore != nil {
		if saveChildSession(deps.SessionStore, spec, req, state, runUsage, remediation) {
			markResultPersisted(&result)
		}
	}
	if err != nil {
		if result != (tool.ExecutionResult{}) {
			return result, nil
		}
		return tool.ExecutionResult{}, fmt.Errorf("%s failed: %w", failureLabel, err)
	}

	result = decorate(result)
	result = applySpecializedWorktreeResult(spec.AgentType, result, worktree, plan.warnings, deps.WorkDir)
	applyFinalizeCancellation(deps.Events, deps.SessionStore, deps.ActiveController, deps.WorkDir, spec.AgentID, &result)
	return result, nil
}

// finishCancelledBeforeDispatch finalises a child cancelled before its runner
// was dispatched: it never ran, but its session is saved so it stays resumable
// state for the caller and cancellation cleanup still applies. A child
// cancelled before provisioning has no request or worktree to save.
func finishCancelledBeforeDispatch(
	deps SubAgentHandlerDeps,
	spec Spec,
	plan *delegatePlan,
	decorate func(tool.ExecutionResult) tool.ExecutionResult,
) tool.ExecutionResult {
	removeAndCloseToolCallTraceWriter(spec.AgentID)
	emitDelegateStopped(deps.Events, spec, spec.AgentType)
	result := cancelledBeforeDispatchResult(spec.AgentID)
	result = decorate(result)
	result = applySpecializedWorktreeResult(spec.AgentType, result, plan.worktree, plan.warnings, deps.WorkDir)
	if plan.ready() && deps.SessionStore != nil && deps.SessionStore.Save(&ChildSession{Spec: spec, Request: plan.req, Remediation: plan.remediation}) {
		markResultPersisted(&result)
	}
	applyFinalizeCancellation(deps.Events, deps.SessionStore, deps.ActiveController, deps.WorkDir, spec.AgentID, &result)
	return result
}
