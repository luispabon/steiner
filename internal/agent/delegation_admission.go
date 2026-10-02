package agent

import (
	"errors"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// errNotDispatched marks calls that never acquired the execution gate and were never launched.
var errNotDispatched = errors.New("tool not dispatched")

// queuedDelegationCalls tracks the delegation calls announced as queued for a
// single tool-execution phase, in emission order, plus which have since been
// dispatched.
type queuedDelegationCalls struct {
	calls   []provider.ToolCall
	started map[string]bool
}

// queueDelegationCalls emits a tool_call_queued event for every delegation-class
// call in the turn, before any of them is dispatched, so a call waiting for a
// parallelism slot is visible in the UI. Regular tool calls are never queued.
// Returns nil when nothing was queued.
func (p *turnProgressor) queueDelegationCalls(turn int, calls []provider.ToolCall) *queuedDelegationCalls {
	if p.request.ParallelClassOf == nil {
		return nil
	}
	var queued *queuedDelegationCalls
	for _, call := range calls {
		if p.request.ParallelClassOf(call.Name) != ParallelClassDelegation {
			continue
		}
		if queued == nil {
			queued = &queuedDelegationCalls{started: make(map[string]bool)}
		}
		queued.calls = append(queued.calls, call)
		emitEvent(p.request.Events, output.NewToolCallQueuedEvent(turn, call.Name, call.ID, cloneInput(call.Arguments)))
	}
	return queued
}

// markDelegationStarted records that a queued delegation call reached dispatch.
func (p *turnProgressor) markDelegationStarted(callID string) {
	if p.queuedDelegations == nil {
		return
	}
	p.queuedDelegations.started[callID] = true
}

// drainQueuedDelegations terminates any queued delegation call that was never
// dispatched with a tool_call_finished error event, so its waiting UI box does
// not linger after the turn stops.
func (p *turnProgressor) drainQueuedDelegations(turn int) {
	queued := p.queuedDelegations
	p.queuedDelegations = nil
	if queued == nil {
		return
	}
	for _, call := range queued.calls {
		if queued.started[call.ID] {
			continue
		}
		admission := p.notDispatchedAdmission(call.Name)
		emitEvent(p.request.Events, output.NewToolCallFinishedEventWithAdmission(turn, call.Name, call.ID, "", errNotDispatched, output.ToolPreview{}, admission))
	}
}

func (p *turnProgressor) emitToolFinished(turn int, call provider.ToolCall, content string, err error, preview output.ToolPreview, admission *tool.DelegationAdmission, emit bool) {
	if !emit {
		return
	}
	emitEvent(p.request.Events, output.NewToolCallFinishedEventWithAdmission(turn, call.Name, call.ID, content, err, preview, admission))
}

func (p *turnProgressor) isDelegationCall(toolName string) bool {
	return p.request.ParallelClassOf != nil && p.request.ParallelClassOf(toolName) == ParallelClassDelegation
}

func (p *turnProgressor) notDispatchedAdmission(toolName string) *tool.DelegationAdmission {
	if !p.isDelegationCall(toolName) {
		return nil
	}
	return &tool.DelegationAdmission{Status: tool.DelegationAdmissionRejected, BatchID: p.batchID}
}

// defaultRejectedAdmission synthesises the rejected admission of a failed
// delegation call that carried none, flagging policy denials.
func (p *turnProgressor) defaultRejectedAdmission(toolName string, err error) *tool.DelegationAdmission {
	admission := p.notDispatchedAdmission(toolName)
	if admission == nil {
		return nil
	}
	var toolErr *tool.ToolExecutionError
	if errors.As(err, &toolErr) && toolErr.Kind == "policy_denied" {
		admission.PolicyNotice = true
	}
	return admission
}

func admissionFromToolResult(result any) *tool.DelegationAdmission {
	execution, ok := result.(tool.ExecutionResult)
	if !ok {
		return nil
	}
	return execution.DelegationAdmission.Clone()
}
