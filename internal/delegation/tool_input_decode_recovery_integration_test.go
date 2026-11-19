package delegation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// scriptedChildProvider replays pre-built responses in order and records every
// request so a test can inspect what the child model saw.
type scriptedChildProvider struct {
	responses []provider.ChatResponse
	requests  []provider.ChatRequest
}

func (p *scriptedChildProvider) ChatCompletion(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	return p.responseFor(len(p.requests))
}

func (p *scriptedChildProvider) responseFor(call int) (provider.ChatResponse, error) {
	if call <= len(p.responses) {
		return p.responses[call-1], nil
	}
	return provider.ChatResponse{}, fmt.Errorf("scripted provider: unexpected request %d", call)
}

func (p *scriptedChildProvider) StreamChatCompletion(ctx context.Context, req provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	resp, err := p.ChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}
	ch := make(chan provider.ChatChunk, 1)
	ch <- provider.ChatChunk{Done: true, FinishReason: resp.FinishReason, Delta: resp.Message}
	close(ch)
	return ch, nil
}

func (*scriptedChildProvider) SupportsUsageStats() bool { return false }

// TestChildGrepLimitSuggestsHeadLimitAndRecovers is the end-to-end regression
// for a child that batches three identical grep input failures with one
// successful glob call. The repeat-failure guard used to advance its streak per
// tool outcome, so three identical failures in a single response exhausted it
// before the model could react, and no corrected call was ever made. This runs
// the real builtin handlers through the real runner with parallel tool calls
// and proves the child sees all four results, reads the decoder's head_limit
// suggestion, retries correctly, and completes.
func TestChildGrepLimitSuggestsHeadLimitAndRecovers(t *testing.T) {
	root := t.TempDir()
	writeRecoveryFixture(t, filepath.Join(root, "main.go"), "package main\n\n// needle in the root file\n")
	writeRecoveryFixture(t, filepath.Join(root, "pkg", "helper.go"), "package pkg\n\n// needle in a nested file\n")

	badGrep := func(id, path string) provider.ToolCall {
		return provider.ToolCall{ID: id, Name: "grep", Arguments: map[string]any{
			"pattern": "needle",
			"path":    path,
			"limit":   1,
		}}
	}
	fixedGrep := func(id, path string) provider.ToolCall {
		return provider.ToolCall{ID: id, Name: "grep", Arguments: map[string]any{
			"pattern":    "needle",
			"path":       path,
			"head_limit": 2,
		}}
	}

	prov := &scriptedChildProvider{responses: []provider.ChatResponse{
		{
			Message: provider.Message{Role: provider.MessageRoleAssistant, ToolCalls: []provider.ToolCall{
				badGrep("call-grep-1", "."),
				badGrep("call-grep-2", "pkg"),
				badGrep("call-grep-3", "."),
				{ID: "call-glob-1", Name: "glob", Arguments: map[string]any{"pattern": "*.go"}},
			}},
			FinishReason: "tool_calls",
		},
		{
			Message: provider.Message{Role: provider.MessageRoleAssistant, ToolCalls: []provider.ToolCall{
				fixedGrep("call-grep-fixed-1", "."),
				fixedGrep("call-grep-fixed-2", "pkg"),
				fixedGrep("call-grep-fixed-3", "."),
			}},
			FinishReason: "tool_calls",
		},
		{
			Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "done"},
			FinishReason: "stop",
		},
	}}

	paths := config.PathsConfig{ProjectRootOnly: true}
	policy := tool.NewPathPolicy(root, paths)
	env := builtin.Env{WorkDir: root, PathPolicy: &policy}
	parent := tool.NewRegistry()
	parent.Register(builtin.NewGrepTool(env))
	parent.Register(builtin.NewGlobTool(env))

	runner := agent.NewRunner()
	deps := SubAgentHandlerDeps{
		Provider:         prov,
		ParentReg:        parent,
		SubAgentCfg:      config.SubAgentConfig{Enabled: true, MaxTurns: config.MinSubAgentMaxTurns, MaxTokens: config.DefaultSubAgentMaxTokens, MaxParallel: 1},
		Events:           output.NoopSink{},
		Runner:           runner,
		WorkDir:          root,
		MaxParallelTools: 4,
		ExecutorConfig:   tool.ExecutorConfigFromConfig(config.Config{Paths: paths}),
		Sandbox:          tool.Unsandboxed{},
	}
	spec := Spec{
		Task:      "search the workspace for needle",
		AgentID:   "agent-grep-guard",
		AgentType: AgentTypeExplore,
		Limits:    Limits{MaxTurns: config.MinSubAgentMaxTurns},
	}
	req, _, err := BuildChildRun(context.Background(), deps, ChildBootstrapOverrides{
		AgentType:    AgentTypeExplore,
		AllowedTools: []string{"grep", "glob"},
		Provider:     prov,
	}, spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}

	result, state, _, err := SpawnDelegate(context.Background(), spec, req, runner, output.NoopSink{}, nil)
	if err != nil {
		t.Fatalf("SpawnDelegate() error = %v", err)
	}

	if len(prov.requests) != 3 {
		t.Fatalf("provider requests = %d, want 3: the guard must not stop the child after one batched failure", len(prov.requests))
	}
	if got := len(toolResultMessages(t, prov.requests[0])); got != 0 {
		t.Fatalf("tool results in first request = %d, want 0", got)
	}

	firstResults := toolResultMessages(t, prov.requests[1])
	if len(firstResults) != 4 {
		t.Fatalf("tool results in second request = %d, want 4 (no skipped results); ids: %v", len(firstResults), toolResultIDs(firstResults))
	}
	byID := toolResultsByID(firstResults)
	for _, id := range []string{"call-grep-1", "call-grep-2", "call-grep-3"} {
		content, ok := byID[id]
		if !ok {
			t.Fatalf("second request is missing tool result %q; ids: %v", id, toolResultIDs(firstResults))
		}
		var envelope tool.JSONEnvelope
		if err := json.Unmarshal([]byte(content), &envelope); err != nil {
			t.Fatalf("tool result %q is not a JSON envelope: %v (%s)", id, err, content)
		}
		if envelope.OK {
			t.Errorf("tool result %q ok = true, want a tool error envelope", id)
		}
		if envelope.Error == nil {
			t.Fatalf("tool result %q has no error payload: %s", id, content)
		}
		if envelope.Error.Kind != "tool_error" {
			t.Errorf("tool result %q error kind = %q, want %q", id, envelope.Error.Kind, "tool_error")
		}
		for _, want := range []string{`unknown field "limit"`, `did you mean "head_limit"?`} {
			if !strings.Contains(envelope.Error.Message, want) {
				t.Errorf("tool result %q error message lacks %q: %s", id, want, envelope.Error.Message)
			}
		}
	}
	globContent, ok := byID["call-glob-1"]
	if !ok {
		t.Fatalf("second request is missing the glob result; ids: %v", toolResultIDs(firstResults))
	}
	if strings.Contains(globContent, `"ok":false`) {
		t.Errorf("glob result is an error envelope, want success: %s", globContent)
	}
	if !strings.Contains(globContent, "main.go") {
		t.Errorf("glob result lacks main.go: %s", globContent)
	}

	thirdResults := toolResultMessages(t, prov.requests[2])
	if len(thirdResults) != 7 {
		t.Fatalf("tool results in third request = %d, want 7 (four first-batch results plus three corrected greps); ids: %v", len(thirdResults), toolResultIDs(thirdResults))
	}
	thirdByID := toolResultsByID(thirdResults)
	for _, id := range []string{"call-grep-fixed-1", "call-grep-fixed-2", "call-grep-fixed-3"} {
		content, ok := thirdByID[id]
		if !ok {
			t.Fatalf("third request is missing tool result %q; ids: %v", id, toolResultIDs(thirdResults))
		}
		if strings.Contains(content, `"ok":false`) || strings.Contains(content, "did you mean") {
			t.Errorf("corrected grep result %q is still an error envelope: %s", id, content)
		}
		if !strings.Contains(content, "needle") {
			t.Errorf("corrected grep result %q lacks the matched content: %s", id, content)
		}
	}

	typed, ok := result.Value.(Result)
	if !ok {
		t.Fatalf("result.Value type = %T, want Result", result.Value)
	}
	if typed.Status != StatusComplete {
		t.Errorf("Status = %q, want %q", typed.Status, StatusComplete)
	}
	if typed.Output != "done" {
		t.Errorf("Output = %q, want %q", typed.Output, "done")
	}
	if typed.TurnCount != 3 {
		t.Errorf("TurnCount = %d, want 3", typed.TurnCount)
	}
	if state.StopReason != agent.StopReasonComplete {
		t.Errorf("StopReason = %q, want %q", state.StopReason, agent.StopReasonComplete)
	}
}

func writeRecoveryFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func toolResultMessages(t *testing.T, req provider.ChatRequest) []provider.Message {
	t.Helper()
	var out []provider.Message
	for _, msg := range req.Messages {
		if msg.Role == provider.MessageRoleTool {
			out = append(out, msg)
		}
	}
	return out
}

func toolResultIDs(messages []provider.Message) []string {
	ids := make([]string, 0, len(messages))
	for _, msg := range messages {
		ids = append(ids, msg.ToolCallID)
	}
	return ids
}

func toolResultsByID(messages []provider.Message) map[string]string {
	byID := make(map[string]string, len(messages))
	for _, msg := range messages {
		byID[msg.ToolCallID] = msg.Content
	}
	return byID
}
