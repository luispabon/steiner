package agent

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// TestBashArtifactCapResultReachesModelMessage runs the real bash handler past
// the artifact cap and feeds its result through executeToolCalls.
func TestBashArtifactCapResultReachesModelMessage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("artifact store is unix only")
	}
	dir := t.TempDir()
	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	bash := builtin.NewBashTool(builtin.Env{WorkDir: dir, PathPolicy: &policy})
	ctx := context.WithValue(context.Background(), tool.SandboxWrapperKey{}, tool.ResolvedSandbox{Wrapper: tool.Unsandboxed{}})
	resI, err := bash.Handler(ctx, map[string]any{"command": "seq 1 400000", "max_output_bytes": 100})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	res, ok := resI.(*builtin.BashResult)
	if !ok {
		t.Fatalf("handler result type = %T, want *builtin.BashResult", resI)
	}
	if !res.ArtifactLimitHit || res.FilePath == "" || res.ArtifactStatus != "partial" {
		t.Fatalf("artifact fields: limit_hit=%v path=%q status=%q", res.ArtifactLimitHit, res.FilePath, res.ArtifactStatus)
	}

	var events []output.Event
	p := newTurnProgressor(RunRequest{
		Executor: bashArtifactExecutor{result: res},
		Events:   output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	}, prompt.AssemblyOptions{}, nil)
	calls := []provider.ToolCall{{ID: "call-1", Name: "bash", Arguments: map[string]any{}}}
	outcome := p.executeToolCalls(context.Background(), RunState{TurnCount: 3, Lineage: newConversationLineage(nil)}, provider.ChatResponse{Message: provider.Message{ToolCalls: calls}})
	if outcome.Stop || outcome.Error != nil {
		t.Fatalf("Stop = %v, Error = %v, want the warning to be nonfatal", outcome.Stop, outcome.Error)
	}

	var artifacts []output.BashArtifactTruncatedEvent
	var finished []output.ToolCallFinishedEvent
	for _, e := range events {
		switch payload := e.Payload.(type) {
		case output.BashArtifactTruncatedEvent:
			artifacts = append(artifacts, payload)
		case output.ToolCallFinishedEvent:
			finished = append(finished, payload)
		}
	}
	if len(artifacts) != 1 {
		t.Fatalf("BashArtifactTruncatedEvent count = %d, want 1", len(artifacts))
	}
	if artifacts[0].FilePath != res.FilePath {
		t.Errorf("event file_path = %q, want %q", artifacts[0].FilePath, res.FilePath)
	}
	if len(finished) != 1 {
		t.Fatalf("ToolCallFinishedEvent count = %d, want 1", len(finished))
	}
	if finished[0].Error != "" {
		t.Errorf("ToolCallFinishedEvent.Error = %q, want empty", finished[0].Error)
	}

	messages := outcome.State.Conversation
	if len(messages) == 0 {
		t.Fatal("conversation is empty, want a tool message")
	}
	last := messages[len(messages)-1]
	if last.Role != MessageRoleTool || last.ToolCallID != "call-1" {
		t.Fatalf("last message role = %q call = %q, want tool message for call-1", last.Role, last.ToolCallID)
	}
	for _, want := range []string{
		`"artifact_limit_hit":true`,
		`"file_path":"` + res.FilePath + `"`,
		`"artifact_status":"partial"`,
	} {
		if !strings.Contains(last.Content, want) {
			t.Errorf("tool message content lacks %s: %s", want, last.Content)
		}
	}
}

func TestBashArtifactTruncatedEventWithoutFilePathUsesFallback(t *testing.T) {
	var events []output.Event
	p := newTurnProgressor(RunRequest{
		Executor: bashArtifactExecutor{result: &builtin.BashResult{ArtifactStatus: "partial", ArtifactLimitHit: true}},
		Events:   output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	}, prompt.AssemblyOptions{}, nil)
	calls := []provider.ToolCall{{ID: "call-1", Name: "bash", Arguments: map[string]any{}}}
	p.executeToolCalls(context.Background(), RunState{TurnCount: 3, Lineage: newConversationLineage(nil)}, provider.ChatResponse{Message: provider.Message{ToolCalls: calls}})

	var artifacts []output.BashArtifactTruncatedEvent
	for _, e := range events {
		if payload, ok := e.Payload.(output.BashArtifactTruncatedEvent); ok {
			artifacts = append(artifacts, payload)
		}
	}
	if len(artifacts) != 1 {
		t.Fatalf("BashArtifactTruncatedEvent count = %d, want 1", len(artifacts))
	}
	if artifacts[0].FilePath != "" {
		t.Errorf("file_path = %q, want empty", artifacts[0].FilePath)
	}
	const wantMessage = "warning: bash artifact truncated; saved output is partial"
	if artifacts[0].Message != wantMessage {
		t.Errorf("message = %q, want %q", artifacts[0].Message, wantMessage)
	}
}

func TestBashArtifactBuildToolMessageEmitFinishedFalseSuppressesWarning(t *testing.T) {
	var events []output.Event
	p := newTurnProgressor(RunRequest{
		Events: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	}, prompt.AssemblyOptions{}, nil)
	call := provider.ToolCall{ID: "call-1", Name: "bash", Arguments: map[string]any{}}
	msg := p.buildToolMessageWithEvent(3, call, bashArtifactLimitHit(), nil, false, nil)

	if !strings.Contains(msg.Content, `"artifact_limit_hit":true`) {
		t.Errorf("tool message content lacks artifact_limit_hit: %s", msg.Content)
	}
	for _, e := range events {
		if e.Type == output.EventTypeBashArtifactTruncated {
			t.Errorf("emitted %s with emitFinished=false, want none", e.Type)
		}
	}
}
