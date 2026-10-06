package agent

import (
	"errors"
	"fmt"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func delegateClassifier(name string) ParallelClass {
	if name == "delegate" {
		return ParallelClassDelegation
	}
	return ParallelClassTool
}

func TestBuildToolMessageDelegationAdmission(t *testing.T) {
	authoritative := &tool.DelegationAdmission{Status: tool.DelegationAdmissionAccepted, BatchID: "batch", Group: "group", AgentID: "agent"}
	rejected := &tool.DelegationAdmission{Status: tool.DelegationAdmissionRejected, BatchID: "batch"}
	for _, test := range []struct {
		name   string
		tool   string
		result any
		err    error
		want   *tool.DelegationAdmission
	}{
		{name: "success without metadata", tool: "delegate", result: "ok", want: &tool.DelegationAdmission{Status: tool.DelegationAdmissionAccepted}},
		{name: "execution result without metadata", tool: "delegate", result: tool.ExecutionResult{Value: "ok"}, want: &tool.DelegationAdmission{Status: tool.DelegationAdmissionAccepted}},
		{name: "success with metadata", tool: "delegate", result: tool.ExecutionResult{Value: "ok", DelegationAdmission: authoritative}, want: authoritative},
		{name: "rejected success metadata survives", tool: "delegate", result: tool.ExecutionResult{Value: "ok", DelegationAdmission: rejected}, want: rejected},
		{name: "handler error without metadata", tool: "delegate", err: errors.New("failure"), want: &tool.DelegationAdmission{Status: tool.DelegationAdmissionRejected}},
		{name: "handler error with error metadata", tool: "delegate", err: tool.WithDelegationAdmission(errors.New("failure"), authoritative), want: authoritative},
		{name: "result metadata survives error", tool: "delegate", result: tool.ExecutionResult{DelegationAdmission: authoritative}, err: errors.New("after admission"), want: authoritative},
		{name: "result metadata overrides error metadata", tool: "delegate", result: tool.ExecutionResult{DelegationAdmission: authoritative}, err: tool.WithDelegationAdmission(errors.New("failure"), rejected), want: authoritative},
		{name: "policy denied", tool: "delegate", err: &tool.ToolExecutionError{Tool: "delegate", Kind: "policy_denied", Message: "blocked"}, want: &tool.DelegationAdmission{Status: tool.DelegationAdmissionRejected, PolicyNotice: true}},
		{name: "approval denied", tool: "delegate", err: fmt.Errorf("approval: %w", &tool.ToolExecutionError{Tool: "delegate", Kind: "policy_denied", Message: "approval denied"}), want: &tool.DelegationAdmission{Status: tool.DelegationAdmissionRejected, PolicyNotice: true}},
		{name: "not dispatched", tool: "delegate", err: errNotDispatched, want: &tool.DelegationAdmission{Status: tool.DelegationAdmissionRejected}},
		{name: "model guidance", tool: "delegate", err: tool.WithModelGuidance(errors.New("dispatch fresh")), want: &tool.DelegationAdmission{Status: tool.DelegationAdmissionRejected, ModelGuidance: true}},
		{name: "wrapped model guidance", tool: "delegate", err: fmt.Errorf("outer: %w", tool.WithModelGuidance(errors.New("dispatch fresh"))), want: &tool.DelegationAdmission{Status: tool.DelegationAdmissionRejected, ModelGuidance: true}},
		{name: "model guidance on supervisor rejection", tool: "delegate", err: tool.WithDelegationAdmission(tool.WithModelGuidance(errors.New("fresh group")), rejected), want: &tool.DelegationAdmission{Status: tool.DelegationAdmissionRejected, BatchID: "batch", ModelGuidance: true}},
		{name: "model guidance never marks accepted", tool: "delegate", result: tool.ExecutionResult{DelegationAdmission: authoritative}, err: tool.WithModelGuidance(errors.New("after admission")), want: authoritative},
		{name: "model guidance on non-delegation tool", tool: "read", err: tool.WithModelGuidance(errors.New("retry"))},
		{name: "non-delegation success", tool: "read", result: "ok"},
		{name: "non-delegation error", tool: "read", err: &tool.ToolExecutionError{Kind: "policy_denied"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var events []output.Event
			p := newTurnProgressor(RunRequest{ParallelClassOf: delegateClassifier, Events: output.SinkFunc(func(e output.Event) { events = append(events, e) })}, prompt.AssemblyOptions{}, nil)
			message := p.buildToolMessage(1, provider.ToolCall{ID: "call", Name: test.tool}, test.result, test.err, nil)
			assertAdmission(t, "message", message.DelegationAdmission, test.want)
			for _, event := range events {
				if event.Type == output.EventTypeToolCallFinished {
					assertAdmission(t, "event", event.Payload.(output.ToolCallFinishedEvent).DelegationAdmission, test.want)
				}
			}
		})
	}
}

func TestSynthesisedRejectedAdmissionCarriesBatchID(t *testing.T) {
	p := newTurnProgressor(RunRequest{ParallelClassOf: delegateClassifier}, prompt.AssemblyOptions{}, nil)
	p.batchID = "batch-1"
	message := p.buildToolMessage(1, provider.ToolCall{ID: "call", Name: "delegate"}, nil, errNotDispatched, nil)
	assertAdmission(t, "message", message.DelegationAdmission, &tool.DelegationAdmission{Status: tool.DelegationAdmissionRejected, BatchID: "batch-1"})
}

func TestModelGuidanceKeepsToolMessageContent(t *testing.T) {
	cause := errors.New(`follow_up: agent "child-6"'s code worktree is no longer usable; delegate a fresh code agent instead of resuming this one`)
	var events []output.Event
	p := newTurnProgressor(RunRequest{ParallelClassOf: delegateClassifier, Events: output.SinkFunc(func(e output.Event) { events = append(events, e) })}, prompt.AssemblyOptions{}, nil)
	plain := p.buildToolMessage(1, provider.ToolCall{ID: "plain", Name: "delegate"}, nil, cause, nil)
	marked := p.buildToolMessage(1, provider.ToolCall{ID: "marked", Name: "delegate"}, nil, tool.WithModelGuidance(cause), nil)
	if marked.Content != plain.Content {
		t.Fatalf("marked content = %q, want model-facing content unchanged %q", marked.Content, plain.Content)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want two tool_call_finished events", len(events))
	}
	if got := events[1].Payload.(output.ToolCallFinishedEvent).Error; got != cause.Error() {
		t.Fatalf("event error = %q, want machine-readable error kept %q", got, cause.Error())
	}
}

func assertAdmission(t *testing.T, label string, got, want *tool.DelegationAdmission) {
	t.Helper()
	if (got == nil) != (want == nil) || got != nil && *got != *want {
		t.Fatalf("%s admission = %#v, want %#v", label, got, want)
	}
}
