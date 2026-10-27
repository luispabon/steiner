package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// bashArtifactExecutor returns the same result and error for every call.
type bashArtifactExecutor struct {
	result any
	err    error
}

func (e bashArtifactExecutor) Execute(_ context.Context, _, _ string, _ map[string]any) (any, error) {
	return e.result, e.err
}

const bashArtifactPath = ".steiner/artifacts/call-1.txt"

func bashArtifactLimitHit() *builtin.BashResult {
	return &builtin.BashResult{ArtifactStatus: "partial", ArtifactLimitHit: true, FilePath: bashArtifactPath}
}

func TestExecuteToolCalls_BashArtifactTruncatedEvent(t *testing.T) {
	limitHit := bashArtifactLimitHit()
	tests := []struct {
		name       string
		result     any
		err        error
		wantEvents int
	}{
		{name: "limit hit emits one event", result: limitHit, wantEvents: 1},
		{name: "limit hit by value emits one event", result: *limitHit, wantEvents: 1},
		{name: "complete artifact emits nothing", result: &builtin.BashResult{ArtifactStatus: "complete"}},
		{name: "failed artifact emits nothing", result: &builtin.BashResult{ArtifactStatus: "failed"}},
		{name: "errored call with limit hit emits nothing", result: limitHit, err: errors.New("exit 1")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var events []output.Event
			p := newTurnProgressor(RunRequest{
				Executor: bashArtifactExecutor{result: tc.result, err: tc.err},
				Events:   output.SinkFunc(func(e output.Event) { events = append(events, e) }),
			}, prompt.AssemblyOptions{}, nil)
			calls := []provider.ToolCall{{ID: "call-1", Name: "bash", Arguments: map[string]any{}}}
			p.executeToolCalls(context.Background(), RunState{TurnCount: 3, Lineage: newConversationLineage(nil)}, provider.ChatResponse{Message: provider.Message{ToolCalls: calls}})

			var artifacts []output.BashArtifactTruncatedEvent
			var finished []output.ToolCallFinishedEvent
			artifactIdx, finishedIdx := -1, -1
			for i, e := range events {
				switch payload := e.Payload.(type) {
				case output.BashArtifactTruncatedEvent:
					artifacts = append(artifacts, payload)
					artifactIdx = i
				case output.ToolCallFinishedEvent:
					finished = append(finished, payload)
					finishedIdx = i
				}
			}
			if len(artifacts) != tc.wantEvents {
				t.Fatalf("BashArtifactTruncatedEvent count = %d, want %d", len(artifacts), tc.wantEvents)
			}
			if len(finished) != 1 {
				t.Fatalf("ToolCallFinishedEvent count = %d, want 1", len(finished))
			}
			if gotErr := finished[0].Error != ""; gotErr != (tc.err != nil) {
				t.Errorf("ToolCallFinishedEvent.Error = %q, want error only when the call errored", finished[0].Error)
			}
			if tc.wantEvents == 0 {
				return
			}
			if artifactIdx < finishedIdx {
				t.Errorf("artifact event index %d precedes finished event index %d, want after", artifactIdx, finishedIdx)
			}
			got := artifacts[0]
			if got.Turn != 3 || got.CallID != "call-1" || got.FilePath != bashArtifactPath {
				t.Errorf("event = turn %d call %q path %q, want turn 3 call call-1 path %q", got.Turn, got.CallID, got.FilePath, bashArtifactPath)
			}
			wantMessage := fmt.Sprintf("warning: bash artifact truncated: partial output saved to %s", bashArtifactPath)
			if got.Message != wantMessage {
				t.Errorf("message = %q, want %q", got.Message, wantMessage)
			}
		})
	}
}

func TestExecuteToolCalls_BashArtifactTruncatedDoesNotTripFailureGuard(t *testing.T) {
	var artifacts int
	p := newTurnProgressor(RunRequest{
		Executor: bashArtifactExecutor{result: bashArtifactLimitHit()},
		Events: output.SinkFunc(func(e output.Event) {
			if e.Type == output.EventTypeBashArtifactTruncated {
				artifacts++
			}
		}),
	}, prompt.AssemblyOptions{}, nil)
	calls := make([]provider.ToolCall, 3)
	for i := range calls {
		calls[i] = provider.ToolCall{ID: fmt.Sprintf("call_%d", i+1), Name: "bash", Arguments: map[string]any{}}
	}
	outcome := p.executeToolCalls(context.Background(), RunState{Lineage: newConversationLineage(nil)}, provider.ChatResponse{Message: provider.Message{ToolCalls: calls}})

	if outcome.Stop || outcome.Error != nil {
		t.Fatalf("Stop = %v, Error = %v, want no stop and no error", outcome.Stop, outcome.Error)
	}
	if p.tripped != nil {
		t.Fatalf("failure guard tripped: %v", p.tripped)
	}
	if artifacts != len(calls) {
		t.Fatalf("artifact events = %d, want %d", artifacts, len(calls))
	}
}
