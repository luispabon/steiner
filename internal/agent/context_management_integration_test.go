package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// scratchpadToolResult builds a JSON scratchpad tool result for use in fake executor responses.
func scratchpadToolResult(goal, plan, step, decisions, files, open, next string) map[string]any {
	return map[string]any{
		"status":    "ok",
		"goal":      goal,
		"plan":      plan,
		"step":      step,
		"decisions": decisions,
		"files":     files,
		"open":      open,
		"next":      next,
	}
}

func TestRunnerSmartContextManagementEndToEndEmitsDiagnostics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 1 answer\nmore detail",
					ToolCalls: []provider.ToolCall{
						{ID: "call_sp1", Name: "scratchpad", Arguments: map[string]any{
							"goal":      "inspect note",
							"plan":      "reread file",
							"step":      "read first pass",
							"decisions": "decided to read file first",
							"files":     "note.txt (read)",
							"open":      "none",
							"next":      "reread note",
						}},
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 2 answer\nmore detail",
					ToolCalls: []provider.ToolCall{
						{ID: "call_sp2", Name: "scratchpad", Arguments: map[string]any{
							"goal":      "inspect note",
							"plan":      "reread file",
							"step":      "compare reread",
							"decisions": "file unchanged",
							"files":     "note.txt (read)",
							"open":      "none",
							"next":      "finish",
						}},
						{ID: "call_2", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 3 answer\nmore detail",
					ToolCalls: []provider.ToolCall{
						{ID: "call_sp3", Name: "scratchpad", Arguments: map[string]any{
							"goal":      "inspect note",
							"plan":      "reread file",
							"step":      "compare reread",
							"decisions": "still unchanged",
							"files":     "note.txt (read)",
							"open":      "none",
							"next":      "finish",
						}},
						{ID: "call_3", Name: "bash", Arguments: map[string]any{"command": "echo done"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 4 answer",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 3},
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(_ context.Context, toolName string, input map[string]any) (any, error) {
			if toolName == "scratchpad" {
				goal, _ := input["goal"].(string)
				plan, _ := input["plan"].(string)
				step, _ := input["step"].(string)
				decisions, _ := input["decisions"].(string)
				files, _ := input["files"].(string)
				open, _ := input["open"].(string)
				next, _ := input["next"].(string)
				return tool.ExecutionResult{
					Value: scratchpadToolResult(goal, plan, step, decisions, files, open, next),
				}, nil
			}
			return tool.ExecutionResult{
				Value: map[string]any{
					"path":        "note.txt",
					"start_line":  1,
					"end_line":    3,
					"total_lines": 3,
					"output":      "one\ntwo\nthree\n",
				},
			}, nil
		},
	}

	manager := NewContextManager("smart", config.ContextManagementConfig{
		MaskingWindowTurns: 1,
		ReadAnnotations:    true,
	})

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider:       providerStub,
		Executor:       executor,
		ContextManager: manager,
		Prompt: prompt.AssemblyOptions{
			Conversation:      []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}},
			ScratchpadEnabled: true,
		},
		Model: "test-model",
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 128,
		},
		Limits: Limits{MaxTurns: 4, MaxTokens: 100},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(providerStub.requests), 4; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}

	// Scratchpad state injected into second request via context state.
	secondRequest := providerStub.requests[1].Messages
	if !messageContentsContain(secondRequest, "goal: inspect note") {
		t.Fatalf("second request missing carried scratchpad goal: %#v", secondRequest)
	}
	if !messageContentsContain(secondRequest, "step: read first pass") {
		t.Fatalf("second request missing carried scratchpad step: %#v", secondRequest)
	}

	thirdRequest := providerStub.requests[2].Messages
	if !messageContentsContain(thirdRequest, "goal: inspect note") {
		t.Fatalf("third request missing carried scratchpad goal: %#v", thirdRequest)
	}
	if !messageContentsContain(thirdRequest, "step: compare reread") {
		t.Fatalf("third request missing updated scratchpad step: %#v", thirdRequest)
	}

	// Masking only applies after the 2-turn grace period, so checks move to the fourth request.
	fourthRequest := providerStub.requests[3].Messages
	if !messageContentsContain(fourthRequest, "goal: inspect note") {
		t.Fatalf("fourth request missing carried scratchpad goal: %#v", fourthRequest)
	}
	if !messageContentsContain(fourthRequest, "turn 1 answer") {
		t.Fatalf("fourth request missing trimmed older assistant content: %#v", fourthRequest)
	}
	if !messageContentsContain(fourthRequest, "tool result") || !messageContentsContain(fourthRequest, "masked") {
		t.Fatalf("fourth request missing masked older tool result: %#v", fourthRequest)
	}
	if !messageContentsContain(fourthRequest, "file unchanged since turn 1") {
		t.Fatalf("fourth request missing unchanged reread annotation: %#v", fourthRequest)
	}

	kinds := contextDiagnosticKinds(events)
	for _, want := range []string{"scratchpad", "file_annotation", "masking", "budget"} {
		if !containsString(kinds, want) {
			t.Fatalf("diagnostic kinds = %v, want %q", kinds, want)
		}
	}

	var sawScratchpadParsed, sawAnnotated, sawMasked, sawTrimmed bool
	for _, event := range events {
		if event.Type != output.EventTypeContextDiagnostics {
			continue
		}
		payload, ok := event.Payload.(output.ContextDiagnosticsEvent)
		if !ok {
			t.Fatalf("diagnostic payload type = %T, want output.ContextDiagnosticsEvent", event.Payload)
		}
		switch payload.Kind {
		case "scratchpad":
			if payload.Parsed {
				sawScratchpadParsed = true
				if strings.TrimSpace(payload.SummaryPreview) == "" {
					t.Fatalf("scratchpad payload = %#v, want summary preview", payload)
				}
			}
		case "file_annotation":
			if payload.Action == "annotated" {
				sawAnnotated = true
				if payload.Reason != "unchanged since turn 1" {
					t.Fatalf("file annotation reason = %q, want unchanged reread", payload.Reason)
				}
			}
		case "masking":
			if payload.Action == "masked" && payload.Tool == "read" {
				sawMasked = true
				if payload.EpochStatus == "" {
					t.Fatal("masked read diagnostic missing epoch status")
				}
			}
			if payload.Action == "trimmed" {
				sawTrimmed = true
			}
		}
	}
	if !sawScratchpadParsed {
		t.Fatal("missing parsed scratchpad diagnostics")
	}
	if !sawAnnotated {
		t.Fatal("missing unchanged reread annotation diagnostic")
	}
	if !sawMasked {
		t.Fatal("missing masked tool result diagnostic")
	}
	if !sawTrimmed {
		t.Fatal("missing trimmed assistant prose diagnostic")
	}
}

func TestRunnerSmartContextManagementInvalidatesReadAfterSameMtimeRewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_w1", Name: "write", Arguments: map[string]any{"path": "note.txt", "content": "one\n"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5},
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_r1", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5},
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_w2", Name: "write", Arguments: map[string]any{"path": "note.txt", "content": "one\n"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5},
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_r2", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 3},
			},
		},
	}

	var preservedModTime time.Time
	executor := &fakeExecutor{
		execute: func(_ context.Context, toolName string, input map[string]any) (any, error) {
			switch toolName {
			case "write":
				content, _ := input["content"].(string)
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					return nil, err
				}
				info, err := os.Stat(path)
				if err != nil {
					return nil, err
				}
				if preservedModTime.IsZero() {
					preservedModTime = info.ModTime()
				} else if err := os.Chtimes(path, preservedModTime, preservedModTime); err != nil {
					return nil, err
				}
				return tool.ExecutionResult{
					Value: map[string]any{
						"path":          "note.txt",
						"bytes_written": len(content),
					},
				}, nil
			case "read":
				return tool.ExecutionResult{
					Value: map[string]any{
						"path":        "note.txt",
						"start_line":  1,
						"end_line":    1,
						"total_lines": 1,
						"output":      "one\n",
					},
				}, nil
			default:
				return nil, nil
			}
		},
	}

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider:       providerStub,
		Executor:       executor,
		ContextManager: NewContextManager("smart", config.ContextManagementConfig{ReadAnnotations: true}),
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}},
		},
		Model: "test-model",
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 128,
		},
		Limits: Limits{MaxTurns: 5, MaxTokens: 100},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}

	var readResults []string
	for _, message := range state.Conversation {
		if message.Role == MessageRoleTool && message.Name == "read" {
			readResults = append(readResults, message.Content)
		}
	}
	if got, want := len(readResults), 2; got != want {
		t.Fatalf("read result count = %d, want %d", got, want)
	}
	if strings.Contains(readResults[1], "file unchanged since turn") {
		t.Fatalf("second read result = %q, want full content after generation bump", readResults[1])
	}

	foundMismatch := false
	for _, event := range events {
		payload, ok := event.Payload.(output.ContextDiagnosticsEvent)
		if !ok || payload.Kind != "file_annotation" || payload.Turn != 4 {
			continue
		}
		if payload.Reason == "generation changed" && containsString(payload.Notes, "mtime_unchanged") {
			foundMismatch = true
		}
	}
	if !foundMismatch {
		t.Fatal("missing generation-changed file annotation diagnostic for second read")
	}
}

func TestRunnerNaiveContextManagementLeavesHistoryUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 1 answer\nmore detail",
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 2 answer\nmore detail",
					ToolCalls: []provider.ToolCall{
						{ID: "call_2", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 3 answer",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 3},
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(context.Context, string, map[string]any) (any, error) {
			return tool.ExecutionResult{
				Value: map[string]any{
					"path":        "note.txt",
					"start_line":  1,
					"end_line":    3,
					"total_lines": 3,
					"output":      "one\ntwo\nthree\n",
				},
			}, nil
		},
	}

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider:       providerStub,
		Executor:       executor,
		ContextManager: &NaiveContextManager{},
		Prompt: prompt.AssemblyOptions{
			Conversation:      []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}},
			ScratchpadEnabled: true,
		},
		Model: "test-model",
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 128,
		},
		Limits: Limits{MaxTurns: 3, MaxTokens: 100},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got, want := len(providerStub.requests), 3; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
	if !messageContentsContain(providerStub.requests[1].Messages, "turn 1 answer\nmore detail") {
		t.Fatalf("naive second request missing raw content: %#v", providerStub.requests[1].Messages)
	}
	if messageContentsContain(providerStub.requests[2].Messages, "older tool result masked") {
		t.Fatalf("naive third request contains masked tool result: %#v", providerStub.requests[2].Messages)
	}
	if !messageContentsContain(providerStub.requests[2].Messages, "turn 1 answer\nmore detail") {
		t.Fatalf("naive third request missing raw older assistant content: %#v", providerStub.requests[2].Messages)
	}

	if got := state.Conversation[1].Content; got != "turn 1 answer\nmore detail" {
		t.Fatalf("naive assistant content = %q, want unchanged", got)
	}

	// Read tool results should not be annotated by naive manager.
	var readResultContent string
	for _, msg := range state.Conversation {
		if msg.Role == MessageRoleTool && msg.Name == "read" {
			readResultContent = msg.Content
			break
		}
	}
	if strings.Contains(readResultContent, "file unchanged since turn 1") {
		t.Fatalf("naive tool result = %q, want no file annotation", readResultContent)
	}

	kinds := contextDiagnosticKinds(events)
	for _, forbidden := range []string{"scratchpad", "file_annotation", "masking"} {
		if containsString(kinds, forbidden) {
			t.Fatalf("naive diagnostics kinds = %v, want no %q", kinds, forbidden)
		}
	}
}

func contextDiagnosticKinds(events []output.Event) []string {
	kinds := make([]string, 0, len(events))
	for _, event := range events {
		if event.Type != output.EventTypeContextDiagnostics {
			continue
		}
		payload, ok := event.Payload.(output.ContextDiagnosticsEvent)
		if !ok {
			continue
		}
		kinds = append(kinds, payload.Kind)
	}
	return kinds
}

// mustMarshalJSON marshals v or fatally fails the test.
func mustMarshalJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return string(b)
}
