package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

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
					Role: provider.MessageRoleAssistant,
					Content: "turn 1 answer\nmore detail\n<scratchpad>\n" +
						"goal: inspect note\n" +
						"plan: reread file\n" +
						"step: read first pass\n" +
						"next: reread note\n" +
						"open: none\n" +
						"</scratchpad>",
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5},
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					Content: "turn 2 answer\nmore detail\n<scratchpad>\n" +
						"step: compare reread\n" +
						"next: finish\n" +
						"</scratchpad>",
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
		Limits: Limits{MaxTurns: 3, MaxTokens: 100},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(providerStub.requests), 3; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}

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
	if !messageContentsContain(thirdRequest, "turn 1 answer") {
		t.Fatalf("third request missing trimmed older assistant content: %#v", thirdRequest)
	}
	if messageContentsContain(thirdRequest, "turn 1 answer\n<scratchpad>") {
		t.Fatalf("third request still contains raw turn 1 scratchpad: %#v", thirdRequest)
	}
	if !messageContentsContain(thirdRequest, "older tool result masked") {
		t.Fatalf("third request missing masked older tool result: %#v", thirdRequest)
	}
	if !messageContentsContain(thirdRequest, "file unchanged since turn 1") {
		t.Fatalf("third request missing unchanged reread annotation: %#v", thirdRequest)
	}

	kinds := contextDiagnosticKinds(events)
	for _, want := range []string{"scratchpad", "file_annotation", "masking", "budget"} {
		if !containsString(kinds, want) {
			t.Fatalf("diagnostic kinds = %v, want %q", kinds, want)
		}
	}

	var sawScratchpadParsed, sawScratchpadFailed, sawAnnotated, sawMasked, sawTrimmed bool
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
			} else {
				sawScratchpadFailed = true
				if strings.TrimSpace(payload.Reason) == "" {
					t.Fatalf("scratchpad payload = %#v, want failure reason", payload)
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
			}
			if payload.Action == "trimmed" {
				sawTrimmed = true
			}
		}
	}
	if !sawScratchpadParsed {
		t.Fatal("missing parsed scratchpad diagnostics")
	}
	if !sawScratchpadFailed {
		t.Fatal("missing failed scratchpad diagnostic")
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
					Role: provider.MessageRoleAssistant,
					Content: "turn 1 answer\nmore detail\n<scratchpad>\n" +
						"goal: inspect note\n" +
						"plan: reread file\n" +
						"step: read first pass\n" +
						"next: reread note\n" +
						"open: none\n" +
						"</scratchpad>",
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5},
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					Content: "turn 2 answer\nmore detail\n<scratchpad>\n" +
						"step: compare reread\n" +
						"next: finish\n" +
						"</scratchpad>",
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
		t.Fatalf("naive second request missing raw scratchpad content: %#v", providerStub.requests[1].Messages)
	}
	if messageContentsContain(providerStub.requests[2].Messages, "older tool result masked") {
		t.Fatalf("naive third request contains masked tool result: %#v", providerStub.requests[2].Messages)
	}
	if !messageContentsContain(providerStub.requests[2].Messages, "turn 1 answer\nmore detail\n<scratchpad>") {
		t.Fatalf("naive third request missing raw older assistant content: %#v", providerStub.requests[2].Messages)
	}

	if !strings.Contains(state.Conversation[1].Content, "<scratchpad>") {
		t.Fatalf("naive assistant content = %q, want raw scratchpad retained", state.Conversation[1].Content)
	}
	if strings.Contains(state.Conversation[2].Content, "file unchanged since turn 1") {
		t.Fatalf("naive tool result = %q, want no file annotation", state.Conversation[2].Content)
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
