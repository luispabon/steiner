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

func assertAdmission(t *testing.T, label string, got, want *tool.DelegationAdmission) {
	t.Helper()
	if (got == nil) != (want == nil) || got != nil && *got != *want {
		t.Fatalf("%s admission = %#v, want %#v", label, got, want)
	}
}
