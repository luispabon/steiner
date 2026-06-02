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
func scratchpadToolResult(intent, decisions, open, next string) map[string]any {
	return map[string]any{
		"status":    "ok",
		"intent":    intent,
		"decisions": decisions,
		"open":      open,
		"next":      next,
	}
}

func minimalScaffoldInferenceRequest(t *testing.T, request provider.ChatRequest) {
	t.Helper()
	if got, want := len(request.Messages), 2; got != want {
		t.Fatalf("scaffold inference messages = %d, want %d", got, want)
	}
	if got, want := rolesOf(request.Messages), []string{"system", "user"}; !equalStrings(got, want) {
		t.Fatalf("scaffold inference roles = %v, want %v", got, want)
	}
}

//nolint:gocyclo
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
							"intent":    "inspect note",
							"decisions": "decided to read file first",
							"open":      "none",
							"next":      "reread note",
						}},
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 2 answer\nmore detail",
					ToolCalls: []provider.ToolCall{
						{ID: "call_sp2", Name: "scratchpad", Arguments: map[string]any{
							"intent":    "inspect note",
							"decisions": "file unchanged",
							"open":      "none",
							"next":      "finish",
						}},
						{ID: "call_2", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 3 answer\nmore detail",
					ToolCalls: []provider.ToolCall{
						{ID: "call_sp3", Name: "scratchpad", Arguments: map[string]any{
							"intent":    "inspect note",
							"decisions": "still unchanged",
							"open":      "none",
							"next":      "finish",
						}},
						{ID: "call_3", Name: "bash", Arguments: map[string]any{"command": "echo done"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 4 answer",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 3, CompletionTokens: 3},
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(_ context.Context, toolName string, _ map[string]any) (any, error) {
			if toolName == "scratchpad" {
				intent := "inspect note"
				decisions := "file unchanged"
				open := "none"
				next := "finish"
				return tool.ExecutionResult{
					Value: scratchpadToolResult(intent, decisions, open, next),
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

	manager := NewContextStateManager(config.ContextManagementConfig{
		MaskingWindowTurns: 1,
		ReadAnnotations:    true,
		ScratchpadMode:     scratchpadModeHybrid,
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
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
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
	if !messageContentsContain(secondRequest, "intent: inspect note") {
		t.Fatalf("second request missing carried scratchpad intent: %#v", secondRequest)
	}
	if !messageContentsContain(secondRequest, "working file: note.txt") {
		t.Fatalf("second request missing carried working file: %#v", secondRequest)
	}

	thirdRequest := providerStub.requests[2].Messages
	if !messageContentsContain(thirdRequest, "intent: inspect note") {
		t.Fatalf("third request missing carried scratchpad intent: %#v", thirdRequest)
	}
	if !messageContentsContain(thirdRequest, "last action: read note.txt") {
		t.Fatalf("third request missing updated scratchpad last action: %#v", thirdRequest)
	}

	// Masking only applies after the 2-turn grace period, so checks move to the fourth request.
	fourthRequest := providerStub.requests[3].Messages
	if !messageContentsContain(fourthRequest, "intent: inspect note") {
		t.Fatalf("fourth request missing carried scratchpad intent: %#v", fourthRequest)
	}
	if !messageContentsContain(fourthRequest, "turn 1 answer") {
		t.Fatalf("fourth request missing trimmed older assistant content: %#v", fourthRequest)
	}
	if !messageContentsContain(fourthRequest, "tool result") || !messageContentsContain(fourthRequest, "masked") {
		t.Fatalf("fourth request missing masked older tool result: %#v", fourthRequest)
	}
	if !messageContentsContain(fourthRequest, "file unchanged") {
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

//nolint:gocyclo
func TestRunnerSmartContextManagementMasksHistoricalDelegateResult(t *testing.T) {
	const fullDelegateOutput = "delegate output with full findings and repository details"
	const hiddenSummary = "hidden summary marker"
	largeTask := strings.Repeat("inspect the repository thoroughly and summarize the findings ", 12)

	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 1 answer",
					ToolCalls: []provider.ToolCall{
						{ID: "call_delegate", Name: "delegate", Arguments: map[string]any{"task": largeTask}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 2 answer",
					ToolCalls: []provider.ToolCall{
						{ID: "call_2", Name: "bash", Arguments: map[string]any{"command": "echo turn 2"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 3 answer",
					ToolCalls: []provider.ToolCall{
						{ID: "call_3", Name: "bash", Arguments: map[string]any{"command": "echo turn 3"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 4 answer",
				},
				FinishReason: "stop",
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(_ context.Context, toolName string, _ map[string]any) (any, error) {
			switch toolName {
			case "delegate":
				return tool.ExecutionResult{
					Value: map[string]any{
						"output": fullDelegateOutput,
					},
					Retention: &tool.ToolRetention{
						Kind:       tool.RetentionKindDelegateSummary,
						Summary:    hiddenSummary,
						AgentID:    "child-1",
						Status:     "complete",
						TurnCount:  1,
						TokenCount: 9,
					},
				}, nil
			default:
				return tool.ExecutionResult{
					Value: map[string]any{
						"output": toolName + " output",
					},
				}, nil
			}
		},
	}

	manager := NewContextStateManager(config.ContextManagementConfig{
		MaskingWindowTurns: 1,
		ScratchpadMode:     scratchpadModeHybrid,
	})

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider:       providerStub,
		Executor:       executor,
		ContextManager: manager,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}},
		},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		Limits:        Limits{MaxTurns: 4, MaxTokens: 100},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := len(providerStub.requests), 4; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}

	fourthRequest := providerStub.requests[3].Messages
	if !messageContentsContain(fourthRequest, hiddenSummary) {
		t.Fatalf("fourth request missing retained delegate summary: %#v", fourthRequest)
	}
	if messageContentsContain(fourthRequest, fullDelegateOutput) {
		t.Fatalf("fourth request leaked full delegate output: %#v", fourthRequest)
	}
	if strings.Contains(mustMarshalJSON(t, fourthRequest), largeTask) {
		t.Fatalf("fourth request leaked large delegate input: %#v", fourthRequest)
	}

	var sawDelegateCall, sawDelegateResult bool
	for _, msg := range fourthRequest {
		for _, call := range msg.ToolCalls {
			if call.ID == "call_delegate" && call.Name == "delegate" {
				sawDelegateCall = true
			}
		}
		if msg.Role == provider.MessageRoleTool && msg.ToolCallID == "call_delegate" && msg.Name == "delegate" {
			sawDelegateResult = true
			if strings.Contains(msg.Content, fullDelegateOutput) {
				t.Fatalf("delegate tool result leaked full output: %#v", msg)
			}
			if !strings.Contains(msg.Content, hiddenSummary) {
				t.Fatalf("delegate tool result = %q, want retained summary", msg.Content)
			}
		}
	}
	if !sawDelegateCall {
		t.Fatal("fourth request missing delegate tool call with original id/name")
	}
	if !sawDelegateResult {
		t.Fatal("fourth request missing paired delegate tool result with matching tool_call_id")
	}

	if got := state.Conversation[2].Retention; got == nil {
		t.Fatal("delegate retention = nil, want durable retained summary")
	} else if got.Summary != hiddenSummary {
		t.Fatalf("delegate retention summary = %q, want %q", got.Summary, hiddenSummary)
	}
}

//nolint:gocyclo
func TestRunnerSmartContextManagementResetsTaskStateOnRedirect(t *testing.T) {
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "commit result",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 3, CompletionTokens: 3},
			},
		},
	}
	manager := NewContextStateManager(config.ContextManagementConfig{
		ScratchpadMode: scratchpadModeHybrid,
	})
	manager.scratchpad.scratchpad = Scratchpad{
		Intent:       "inspect note",
		Decisions:    "old decision",
		Open:         "why does it fail?",
		Next:         "read note again",
		WorkingFile:  "note.txt",
		LastAction:   "read note.txt",
		SessionState: "session state: turn=8 compactions=2",
	}

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider:       providerStub,
		Executor:       &fakeExecutor{},
		ContextManager: manager,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "commit changes", Turn: 7}},
		},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 128,
		},
		Limits: Limits{MaxTurns: 8, MaxTokens: 100},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := len(providerStub.requests), 1; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
	request := providerStub.requests[0].Messages
	joined := strings.Builder{}
	for i, message := range request {
		if i > 0 {
			joined.WriteString("\n\n")
		}
		joined.WriteString(message.Content)
	}
	if got := strings.Count(joined.String(), "[Current task state]"); got != 1 {
		t.Fatalf("scratchpad block count = %d, want 1 in %#v", got, request)
	}
	if !messageContentsContain(request, "session state: turn=7 compactions=0") {
		t.Fatalf("request missing session state: %#v", request)
	}
	for _, forbidden := range []string{"inspect note", "old decision", "why does it fail?", "read note again", "working file: note.txt", "last action: read note.txt", "goal:", "plan:", "step:", "files:"} {
		if messageContentsContain(request, forbidden) {
			t.Fatalf("request still contains stale task state %q: %#v", forbidden, request)
		}
	}
	if got := manager.scratchpad.scratchpad.Intent; got != "" {
		t.Fatalf("scratchpad intent = %q, want cleared", got)
	}
	if got := manager.scratchpad.scratchpad.WorkingFile; got != "" {
		t.Fatalf("scratchpad working file = %q, want cleared", got)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
}

//nolint:gocyclo
func TestRunnerScaffoldOnlyInferenceTriggersOnFirstAndSteadyTurns(t *testing.T) {
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
					Content: "turn 1 answer",
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: mustMarshalJSON(t, map[string]any{"intent": "inspect note", "next": "reread note"}),
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 2 answer",
					ToolCalls: []provider.ToolCall{
						{ID: "call_2", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 3 answer",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(_ context.Context, toolName string, input map[string]any) (any, error) {
			if toolName != "read" {
				return nil, nil
			}
			path, _ := input["path"].(string)
			return tool.ExecutionResult{
				Value: map[string]any{
					"path":        path,
					"start_line":  1,
					"end_line":    3,
					"total_lines": 3,
					"output":      "one\ntwo\nthree\n",
				},
			}, nil
		},
	}

	manager := NewContextStateManager(config.ContextManagementConfig{ScratchpadMode: scratchpadModeScaffoldOnly})
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider:       providerStub,
		Executor:       executor,
		ContextManager: manager,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}},
		},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 128,
		},
		Limits: Limits{MaxTurns: 3, MaxTokens: 100},
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

	minimalScaffoldInferenceRequest(t, providerStub.requests[1])
	if content := providerStub.requests[1].Messages[1].Content; !strings.Contains(content, "Current scaffold state") || !strings.Contains(content, "Last assistant response") {
		t.Fatalf("scaffold inference prompt = %q, want scaffold state and last response sections", content)
	}
	if !messageContentsContain(providerStub.requests[2].Messages, "intent: inspect note") {
		t.Fatalf("steady turn missing carried scaffold intent: %#v", providerStub.requests[2].Messages)
	}
	if !messageContentsContain(providerStub.requests[2].Messages, "next: reread note") {
		t.Fatalf("steady turn missing carried scaffold next: %#v", providerStub.requests[2].Messages)
	}
	if got := len(providerStub.requests[2].Messages); got <= 2 {
		t.Fatalf("steady turn messages = %d, want assembled conversation path", got)
	}
}

//nolint:gocyclo
func TestRunnerScaffoldOnlyInferenceRunsAfterCompaction(t *testing.T) {
	longText := strings.Repeat("very long context ", 200)
	shortText := "short"

	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "post-compaction answer",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 4, CompletionTokens: 4},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: mustMarshalJSON(t, map[string]any{"intent": "resume work", "next": "continue"}),
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
			},
		},
	}

	initialConversation := []provider.Message{
		{Role: provider.MessageRoleUser, Content: longText},
		{Role: provider.MessageRoleAssistant, Content: longText},
		{Role: provider.MessageRoleUser, Content: shortText},
		{Role: provider.MessageRoleAssistant, Content: shortText},
		{Role: provider.MessageRoleUser, Content: shortText},
		{Role: provider.MessageRoleAssistant, Content: shortText},
		{Role: provider.MessageRoleUser, Content: shortText},
		{Role: provider.MessageRoleAssistant, Content: shortText},
	}

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider:       providerStub,
		Executor:       &fakeExecutor{},
		ContextManager: NewContextStateManager(config.ContextManagementConfig{CompactionStrategy: compactionStrategyDrop, ScratchpadMode: scratchpadModeScaffoldOnly}),
		Prompt: prompt.AssemblyOptions{
			Conversation: initialConversation,
		},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         1024,
			MaxCompletionTokens: 128,
		},
		Limits: Limits{MaxTurns: 1, MaxTokens: 100},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(providerStub.requests), 2; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
	minimalScaffoldInferenceRequest(t, providerStub.requests[1])

	sawCompaction := false
	for _, event := range events {
		if event.Type != output.EventTypeContextDiagnostics {
			continue
		}
		payload, ok := event.Payload.(output.ContextDiagnosticsEvent)
		if !ok {
			continue
		}
		if payload.Kind == "compaction" {
			sawCompaction = true
			break
		}
	}
	if !sawCompaction {
		t.Fatal("missing compaction diagnostic before scaffold inference")
	}
}

//nolint:gocyclo
func TestRunnerScaffoldOnlyInferenceCarriesForwardIntentAndNextOnParseFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	otherPath := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(otherPath, []byte("alpha\nbeta\n"), 0o644); err != nil {
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
					Content: "turn 1 answer",
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: mustMarshalJSON(t, map[string]any{"intent": "inspect note", "next": "reread note"}),
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 2 answer",
					ToolCalls: []provider.ToolCall{
						{ID: "call_2", Name: "read", Arguments: map[string]any{"path": "other.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "not json",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 3 answer",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
			},
		},
	}
	executor := &fakeExecutor{
		execute: func(_ context.Context, toolName string, input map[string]any) (any, error) {
			if toolName != "read" {
				return nil, nil
			}
			path, _ := input["path"].(string)
			return tool.ExecutionResult{
				Value: map[string]any{
					"path":        path,
					"start_line":  1,
					"end_line":    2,
					"total_lines": 2,
					"output":      "line 1\nline 2\n",
				},
			}, nil
		},
	}

	cm := NewContextStateManager(config.ContextManagementConfig{ScratchpadMode: scratchpadModeScaffoldOnly})
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider:       providerStub,
		Executor:       executor,
		ContextManager: cm,
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}},
		},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         4096,
			MaxCompletionTokens: 128,
		},
		Limits: Limits{MaxTurns: 3, MaxTokens: 100},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := state.StopReason, StopReasonComplete; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := len(providerStub.requests), 5; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
	minimalScaffoldInferenceRequest(t, providerStub.requests[1])
	minimalScaffoldInferenceRequest(t, providerStub.requests[3])
	if !messageContentsContain(providerStub.requests[4].Messages, "intent: inspect note") {
		t.Fatalf("post-failure turn missing carried scaffold intent: %#v", providerStub.requests[4].Messages)
	}
	if !messageContentsContain(providerStub.requests[4].Messages, "next: reread note") {
		t.Fatalf("post-failure turn missing carried scaffold next: %#v", providerStub.requests[4].Messages)
	}
	if got := cm.scratchpad.scratchpad.Intent; got != "inspect note" {
		t.Fatalf("scratchpad intent = %q, want inspect note", got)
	}
	if got := cm.scratchpad.scratchpad.Next; got != "reread note" {
		t.Fatalf("scratchpad next = %q, want reread note", got)
	}
}

//nolint:gocyclo
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

	mutateWriteArgs := map[string]any{
		"operations": []any{map[string]any{"type": "write", "path": "note.txt", "content": "one\n"}},
	}
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_w1", Name: "mutate", Arguments: mutateWriteArgs},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_r1", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_w2", Name: "mutate", Arguments: mutateWriteArgs},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_r2", Name: "read", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "done",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 3, CompletionTokens: 3},
			},
		},
	}

	var preservedModTime time.Time
	executor := &fakeExecutor{
		execute: func(_ context.Context, toolName string, input map[string]any) (any, error) {
			switch toolName {
			case "mutate":
				ops, _ := input["operations"].([]any)
				for _, rawOp := range ops {
					op, _ := rawOp.(map[string]any)
					content, _ := op["content"].(string)
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
				}
				return tool.ExecutionResult{
					Value: map[string]any{
						"paths":              []string{"note.txt"},
						"modified":           []string{"note.txt"},
						"operations_applied": 1,
						"operations_failed":  0,
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
		ContextManager: NewContextStateManager(config.ContextManagementConfig{ReadAnnotations: true, ScratchpadMode: scratchpadModeHybrid}),
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}},
		},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
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

//nolint:gocyclo
func TestRunnerContextManagementKeepsAssistantHistoryUntouched(t *testing.T) {
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
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
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
				Usage:        &provider.UsageStats{TotalTokens: 5, CompletionTokens: 5},
			},
			{
				Message: provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "turn 3 answer",
				},
				FinishReason: "stop",
				Usage:        &provider.UsageStats{TotalTokens: 3, CompletionTokens: 3},
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
		ContextManager: NewContextStateManager(),
		Prompt: prompt.AssemblyOptions{
			Conversation:      []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}},
			ScratchpadEnabled: true,
		},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
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

	// The latest repeated read should still be annotated by the baseline manager.
	var readResults []string
	for _, msg := range state.Conversation {
		if msg.Role == MessageRoleTool && msg.Name == "read" {
			readResults = append(readResults, msg.Content)
		}
	}
	if len(readResults) != 2 {
		t.Fatalf("read results = %d, want 2", len(readResults))
	}
	if !strings.Contains(readResults[1], "file unchanged since turn 1") {
		t.Fatalf("second tool result = %q, want unchanged-file annotation", readResults[1])
	}

	kinds := contextDiagnosticKinds(events)
	for _, forbidden := range []string{"scratchpad", "masking"} {
		if containsString(kinds, forbidden) {
			t.Fatalf("diagnostics kinds = %v, want no %q", kinds, forbidden)
		}
	}
	if !containsString(kinds, "file_annotation") {
		t.Fatalf("diagnostics kinds = %v, want file_annotation", kinds)
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
