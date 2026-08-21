package advisor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/usagestats"
)

type fakeProvider struct {
	requests       []provider.ChatRequest
	streamRequests []provider.ChatRequest
	response       provider.ChatResponse
	err            error
	streamChunks   []provider.ChatChunk
	streamErr      error
}

func (p *fakeProvider) ChatCompletion(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if p.err != nil {
		return provider.ChatResponse{}, p.err
	}
	return p.response, nil
}

func (p *fakeProvider) StreamChatCompletion(_ context.Context, req provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	p.streamRequests = append(p.streamRequests, req)
	if p.streamChunks != nil {
		ch := make(chan provider.ChatChunk, len(p.streamChunks))
		for _, c := range p.streamChunks {
			ch <- c
		}
		close(ch)
		return ch, nil
	}
	if p.streamErr != nil {
		return nil, p.streamErr
	}
	return nil, errors.New("unexpected streaming call")
}

func (p *fakeProvider) SupportsUsageStats() bool { return true }

func TestAdviseUsesConversationSnapshotUnmodified(t *testing.T) {
	snapshot := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "fix the failing test"},
		{
			Role: provider.MessageRoleAssistant,
			ToolCalls: []provider.ToolCall{{
				ID:   "call-1",
				Name: "read",
				Arguments: map[string]any{
					"path": "internal/config/config.go",
				},
			}},
		},
		{
			Role:       provider.MessageRoleTool,
			Name:       "read",
			ToolCallID: "call-1",
			Content:    "package config",
		},
	}
	prov := &fakeProvider{
		response: provider.ChatResponse{
			Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "Check config validation before touching agent flow."},
		},
	}

	resp, err := advise(context.Background(), prov, provider.ResolvedModel{BackendModelID: "advisor-model"}, snapshot, "", nil, intPtr(256), nil, "")
	if err != nil {
		t.Fatalf("advise() error = %v", err)
	}
	if got, want := resp.Message.Content, "Check config validation before touching agent flow."; got != want {
		t.Fatalf("response content = %q, want %q", got, want)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("len(requests) = %d, want 1", len(prov.requests))
	}

	req := prov.requests[0]
	if got, want := req.Model, "advisor-model"; got != want {
		t.Fatalf("request.Model = %q, want %q", got, want)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 256 {
		t.Fatalf("request.MaxTokens = %#v, want 256", req.MaxTokens)
	}
	if len(req.Tools) != 0 {
		t.Fatalf("request.Tools = %#v, want nil/empty", req.Tools)
	}
	if got, want := len(req.Messages), len(snapshot)+2; got != want {
		t.Fatalf("len(request.Messages) = %d, want %d", got, want)
	}
	if req.Messages[0].Role != provider.MessageRoleSystem || !strings.Contains(req.Messages[0].Content, "internal advisor") {
		t.Fatalf("request.Messages[0] = %#v, want advisor system prompt", req.Messages[0])
	}

	// snapshot[0]: user message passes through unchanged
	if got := req.Messages[1]; got.Role != provider.MessageRoleUser || got.Content != "fix the failing test" {
		t.Fatalf("request.Messages[1] = %#v, want user pass-through", got)
	}

	// snapshot[1]: assistant tool calls flattened to text, ToolCalls cleared
	assistantMsg := req.Messages[2]
	if assistantMsg.Role != provider.MessageRoleAssistant {
		t.Fatalf("request.Messages[2].Role = %q, want assistant", assistantMsg.Role)
	}
	if len(assistantMsg.ToolCalls) != 0 {
		t.Fatalf("request.Messages[2].ToolCalls not cleared: %#v", assistantMsg.ToolCalls)
	}
	if !strings.Contains(assistantMsg.Content, "[tool_call: read") {
		t.Fatalf("request.Messages[2].Content missing tool_call line: %q", assistantMsg.Content)
	}

	// snapshot[2]: tool result flattened to user message, ToolCallID and Name cleared
	toolResultMsg := req.Messages[3]
	if toolResultMsg.Role != provider.MessageRoleUser {
		t.Fatalf("request.Messages[3].Role = %q, want user", toolResultMsg.Role)
	}
	if !strings.HasPrefix(toolResultMsg.Content, "[tool_result: read]") {
		t.Fatalf("request.Messages[3].Content missing prefix: %q", toolResultMsg.Content)
	}
	if !strings.Contains(toolResultMsg.Content, "package config") {
		t.Fatalf("request.Messages[3].Content missing original content: %q", toolResultMsg.Content)
	}
	if toolResultMsg.ToolCallID != "" {
		t.Fatalf("request.Messages[3].ToolCallID not cleared: %q", toolResultMsg.ToolCallID)
	}
	if toolResultMsg.Name != "" {
		t.Fatalf("request.Messages[3].Name not cleared: %q", toolResultMsg.Name)
	}

	last := req.Messages[len(req.Messages)-1]
	if last.Role != provider.MessageRoleUser || !strings.Contains(last.Content, "advisory note") {
		t.Fatalf("last advisor message = %#v, want advisor user prompt", last)
	}

	// snapshot must not be mutated by flattening
	req.Messages[1].Content = "mutated"
	if snapshot[0].Content != "fix the failing test" {
		t.Fatalf("snapshot[0] mutated to %q, want original", snapshot[0].Content)
	}
	if len(snapshot[1].ToolCalls) != 1 {
		t.Fatalf("snapshot[1].ToolCalls mutated, want original tool calls preserved")
	}
	if snapshot[2].ToolCallID != "call-1" {
		t.Fatalf("snapshot[2].ToolCallID mutated to %q, want original", snapshot[2].ToolCallID)
	}
}

func TestAdviseWrapsProviderErrors(t *testing.T) {
	prov := &fakeProvider{err: errors.New("backend failed")}

	_, err := advise(context.Background(), prov, provider.ResolvedModel{BackendModelID: "advisor-model"}, nil, "", nil, nil, nil, "")
	if err == nil {
		t.Fatal("advise() error = nil, want wrapped error")
	}
	if got := err.Error(); !strings.Contains(got, "advisor: backend failed") {
		t.Fatalf("advise() error = %q, want wrapped provider error", got)
	}
}

func TestAdviseFallsBackToStreamingWhenStreamRequired(t *testing.T) {
	prov := &fakeProvider{
		err: &provider.HTTPError{StatusCode: 400, Body: "Stream must be set to true"},
		streamChunks: []provider.ChatChunk{
			{Delta: provider.Message{Content: "Hello "}},
			{Done: true, Delta: provider.Message{Content: "Hello world"}, FinishReason: "stop"},
		},
	}

	resp, err := advise(context.Background(), prov, provider.ResolvedModel{BackendModelID: "advisor-model"}, nil, "", nil, nil, nil, "")
	if err != nil {
		t.Fatalf("advise() error = %v", err)
	}
	if got, want := resp.Message.Content, "Hello world"; got != want {
		t.Fatalf("response content = %q, want %q", got, want)
	}
	if got, want := resp.FinishReason, "stop"; got != want {
		t.Fatalf("response FinishReason = %q, want %q", got, want)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("len(requests) = %d, want 1 (ChatCompletion was attempted)", len(prov.requests))
	}
}

func TestAdviseStreamRequiredButStreamingFails(t *testing.T) {
	prov := &fakeProvider{
		err:       &provider.HTTPError{StatusCode: 400, Body: "Stream must be set to true"},
		streamErr: errors.New("stream broke"),
	}

	_, err := advise(context.Background(), prov, provider.ResolvedModel{BackendModelID: "advisor-model"}, nil, "", nil, nil, nil, "")
	if err == nil {
		t.Fatal("advise() error = nil, want wrapped error")
	}
	if got := err.Error(); !strings.Contains(got, "advisor: stream broke") {
		t.Fatalf("advise() error = %q, want wrapped streaming error", got)
	}
}

func TestAdviseWithReasoningDirectsToStream(t *testing.T) {
	prov := &fakeProvider{
		streamChunks: []provider.ChatChunk{
			{Delta: provider.Message{Content: "reasoned answer"}},
			{Done: true, Delta: provider.Message{Content: "reasoned answer"}, FinishReason: "stop"},
		},
	}

	rm := provider.ResolvedModel{
		BackendModelID:           "glm-5.2",
		ReasoningEffectiveEffort: "high",
	}

	resp, err := advise(context.Background(), prov, rm, nil, "", nil, nil, nil, "")
	if err != nil {
		t.Fatalf("advise() error = %v", err)
	}
	if got, want := resp.Message.Content, "reasoned answer"; got != want {
		t.Fatalf("response content = %q, want %q", got, want)
	}
	if len(prov.requests) != 0 {
		t.Fatalf("len(ChatCompletion requests) = %d, want 0 (stream used directly)", len(prov.requests))
	}
	if len(prov.streamRequests) != 1 {
		t.Fatalf("len(stream requests) = %d, want 1", len(prov.streamRequests))
	}
	streamReq := prov.streamRequests[0]
	if streamReq.Reasoning == nil {
		t.Fatal("stream request.Reasoning = nil, want non-nil")
	}
	if got, want := streamReq.Reasoning.Effort, "high"; got != want {
		t.Fatalf("stream request.Reasoning.Effort = %q, want %q", got, want)
	}
	if got, want := streamReq.Model, "glm-5.2"; got != want {
		t.Fatalf("stream request.Model = %q, want %q", got, want)
	}
}

func TestAdviseWithReasoningStreamError(t *testing.T) {
	prov := &fakeProvider{
		streamErr: errors.New("stream failed"),
	}

	rm := provider.ResolvedModel{
		BackendModelID:           "glm-5.2",
		ReasoningEffectiveEffort: "high",
	}

	_, err := advise(context.Background(), prov, rm, nil, "", nil, nil, nil, "")
	if err == nil {
		t.Fatal("advise() error = nil, want wrapped error")
	}
	if got := err.Error(); !strings.Contains(got, "advisor: stream failed") {
		t.Fatalf("advise() error = %q, want wrapped stream error", got)
	}
}

func TestAdviseWithReasoningEmitsThinkingChunks(t *testing.T) {
	prov := &fakeProvider{
		streamChunks: []provider.ChatChunk{
			{Delta: provider.Message{Content: ""}, Thinking: "Let me think about this "},
			{Delta: provider.Message{Content: ""}, Thinking: "more deeply"},
			{Done: true, Delta: provider.Message{Content: "Final answer"}, FinishReason: "stop"},
		},
	}

	rm := provider.ResolvedModel{
		BackendModelID:           "glm-5.2",
		ReasoningEffectiveEffort: "high",
	}

	sink := &toolSink{}
	resp, err := advise(context.Background(), prov, rm, nil, "", nil, nil, sink, "")
	if err != nil {
		t.Fatalf("advise() error = %v", err)
	}
	if got, want := resp.Message.Content, "Final answer"; got != want {
		t.Fatalf("response content = %q, want %q", got, want)
	}

	var gotThinking []string
	for _, e := range sink.events {
		if p, ok := e.Payload.(output.ThinkingChunkEvent); ok {
			gotThinking = append(gotThinking, p.Content)
		}
	}
	wantThinking := []string{"Let me think about this ", "more deeply"}
	if len(gotThinking) != len(wantThinking) {
		t.Fatalf("thinking events = %v, want %v", gotThinking, wantThinking)
	}
	for i := range gotThinking {
		if gotThinking[i] != wantThinking[i] {
			t.Fatalf("thinking event[%d] = %q, want %q", i, gotThinking[i], wantThinking[i])
		}
	}
}

func contextWithSnapshot(snapshot []provider.Message) context.Context {
	return agent.WithConversationSnapshot(context.Background(), snapshot)
}

func TestCacheKeyReusedAcrossHandlerCalls(t *testing.T) {
	prov := &fakeProvider{
		response: provider.ChatResponse{
			Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "advice 1"},
		},
	}

	handler := NewHandler(HandlerDeps{
		Provider: prov,
		Model:    provider.ResolvedModel{BackendModelID: "test-model"},
		Events:   nil,
		Config:   Config{MaxUsesPerRun: 10},
	})

	snapshot := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "test question"},
	}
	ctx := contextWithSnapshot(snapshot)

	// First call
	_, err := handler(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("first handler call error = %v", err)
	}

	// Second call with same context
	_, err = handler(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("second handler call error = %v", err)
	}

	if len(prov.requests) < 2 {
		t.Fatalf("len(requests) = %d, want at least 2", len(prov.requests))
	}

	key1 := prov.requests[0].PromptCacheKey
	key2 := prov.requests[1].PromptCacheKey

	if key1 == "" {
		t.Fatalf("first request PromptCacheKey is empty, want non-empty")
	}
	if key1 != key2 {
		t.Fatalf("cache keys differ: %q vs %q, want identical", key1, key2)
	}
}

func TestDifferentHandlersDifferentCacheKeys(t *testing.T) {
	prov1 := &fakeProvider{
		response: provider.ChatResponse{
			Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "advice 1"},
		},
	}
	prov2 := &fakeProvider{
		response: provider.ChatResponse{
			Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "advice 2"},
		},
	}

	handler1 := NewHandler(HandlerDeps{
		Provider: prov1,
		Model:    provider.ResolvedModel{BackendModelID: "test-model"},
		Events:   nil,
		Config:   Config{MaxUsesPerRun: 10},
	})

	handler2 := NewHandler(HandlerDeps{
		Provider: prov2,
		Model:    provider.ResolvedModel{BackendModelID: "test-model"},
		Events:   nil,
		Config:   Config{MaxUsesPerRun: 10},
	})

	snapshot := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "test question"},
	}
	ctx := contextWithSnapshot(snapshot)

	_, err := handler1(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("handler1 call error = %v", err)
	}

	_, err = handler2(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("handler2 call error = %v", err)
	}

	if len(prov1.requests) != 1 {
		t.Fatalf("len(prov1.requests) = %d, want 1", len(prov1.requests))
	}
	if len(prov2.requests) != 1 {
		t.Fatalf("len(prov2.requests) = %d, want 1", len(prov2.requests))
	}

	key1 := prov1.requests[0].PromptCacheKey
	key2 := prov2.requests[0].PromptCacheKey

	if key1 == "" {
		t.Fatalf("handler1 PromptCacheKey is empty, want non-empty")
	}
	if key2 == "" {
		t.Fatalf("handler2 PromptCacheKey is empty, want non-empty")
	}
	if key1 == key2 {
		t.Fatalf("cache keys are identical: %q, want different", key1)
	}
}

func TestAdviseSucceedsWithEmptyCacheKey(t *testing.T) {
	prov := &fakeProvider{
		response: provider.ChatResponse{
			Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "advice works"},
		},
	}

	resp, err := advise(context.Background(), prov, provider.ResolvedModel{BackendModelID: "test-model"}, nil, "", nil, nil, nil, "")
	if err != nil {
		t.Fatalf("advise() error = %v", err)
	}
	if got, want := resp.Message.Content, "advice works"; got != want {
		t.Fatalf("response content = %q, want %q", got, want)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("len(requests) = %d, want 1", len(prov.requests))
	}
	// Empty key should be set (or unset, but field is there)
	if prov.requests[0].PromptCacheKey != "" {
		t.Fatalf("request PromptCacheKey = %q, want empty string", prov.requests[0].PromptCacheKey)
	}
	if !prov.requests[0].AdvisorCacheProfile {
		t.Fatal("request AdvisorCacheProfile = false, want true for every advisor call")
	}
}

func intPtr(v int) *int { return &v }

func TestHandlerRecordsUsageStatsOnNonStreamingCall(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	prov := &fakeProvider{
		response: provider.ChatResponse{
			Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "advice"},
			Usage: &provider.UsageStats{
				PromptTokens:             500,
				CompletionTokens:         50,
				CacheReadInputTokens:     300,
				CacheCreationInputTokens: 20,
			},
		},
	}
	rec := usagestats.New(nil)
	const backendModelID = "advisor-nonstream-usage-test-model"
	handler := NewHandler(HandlerDeps{
		Provider:      prov,
		Model:         provider.ResolvedModel{BackendModelID: backendModelID, ProviderAlias: "test-alias"},
		Config:        Config{MaxUsesPerRun: 1},
		UsageRecorder: rec,
	})

	ctx := contextWithSnapshot([]provider.Message{{Role: provider.MessageRoleUser, Content: "fix it"}})
	if _, err := handler(ctx, nil); err != nil {
		t.Fatalf("handler() error = %v", err)
	}

	row := findUsageRow(t, rec, backendModelID)
	if row.CacheReadTokens != 300 || row.CacheCreateTokens != 20 || row.CompletionTokens != 50 {
		t.Fatalf("row = %#v, want cache read=300 create=20 completion=50", row)
	}
	if row.ProviderAlias != "test-alias" {
		t.Fatalf("row.ProviderAlias = %q, want test-alias", row.ProviderAlias)
	}
}

func TestHandlerRecordsUsageStatsOnStreamingCall(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	prov := &fakeProvider{
		streamChunks: []provider.ChatChunk{
			{Delta: provider.Message{Content: "reasoned "}},
			{
				Done:         true,
				Delta:        provider.Message{Content: "reasoned answer"},
				FinishReason: "stop",
				Usage: &provider.UsageStats{
					PromptTokens:             800,
					CompletionTokens:         40,
					CacheReadInputTokens:     600,
					CacheCreationInputTokens: 10,
				},
			},
		},
	}
	rec := usagestats.New(nil)
	const backendModelID = "advisor-stream-usage-test-model"
	handler := NewHandler(HandlerDeps{
		Provider: prov,
		Model: provider.ResolvedModel{
			BackendModelID:           backendModelID,
			ReasoningEffectiveEffort: "high",
		},
		Config:        Config{MaxUsesPerRun: 1},
		UsageRecorder: rec,
	})

	ctx := contextWithSnapshot([]provider.Message{{Role: provider.MessageRoleUser, Content: "fix it"}})
	if _, err := handler(ctx, nil); err != nil {
		t.Fatalf("handler() error = %v", err)
	}

	row := findUsageRow(t, rec, backendModelID)
	if row.CacheReadTokens != 600 || row.CacheCreateTokens != 10 || row.CompletionTokens != 40 {
		t.Fatalf("row = %#v, want cache read=600 create=10 completion=40", row)
	}
}

// findUsageRow returns the single report row matching backendModelID, failing
// the test if it is absent or duplicated.
func findUsageRow(t *testing.T, rec *usagestats.Recorder, backendModelID string) usagestats.Row {
	t.Helper()
	report := rec.Window(time.Hour)
	var matches []usagestats.Row
	for _, row := range report.Rows {
		if row.BackendModelID == backendModelID {
			matches = append(matches, row)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("rows matching BackendModelID %q = %d, want 1 (rows: %#v)", backendModelID, len(matches), report.Rows)
	}
	return matches[0]
}

func TestAdvisorCompleteEventCarriesCacheTokenCounts(t *testing.T) {
	t.Parallel()

	prov := &fakeProvider{
		response: provider.ChatResponse{
			Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "advice"},
			Usage: &provider.UsageStats{
				CacheReadInputTokens:     150,
				CacheCreationInputTokens: 5,
			},
		},
	}
	sink := &toolSink{}
	handler := NewHandler(HandlerDeps{
		Provider: prov,
		Model:    provider.ResolvedModel{BackendModelID: "advisor-model"},
		Events:   sink,
		Config:   Config{MaxUsesPerRun: 1},
	})

	ctx := contextWithSnapshot([]provider.Message{{Role: provider.MessageRoleUser, Content: "fix it"}})
	if _, err := handler(ctx, nil); err != nil {
		t.Fatalf("handler() error = %v", err)
	}

	completed, ok := sink.events[1].Payload.(output.AdvisorCompleteEvent)
	if !ok {
		t.Fatalf("event[1] payload = %T, want AdvisorCompleteEvent", sink.events[1].Payload)
	}
	if completed.CacheReadTokens != 150 || completed.CacheCreateTokens != 5 {
		t.Fatalf("completed cache tokens = %#v, want read=150 create=5", completed)
	}
}

func TestHandlerWithNilUsageRecorderDoesNotPanic(t *testing.T) {
	t.Parallel()

	prov := &fakeProvider{
		response: provider.ChatResponse{
			Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "advice"},
			Usage: &provider.UsageStats{
				CacheReadInputTokens:     150,
				CacheCreationInputTokens: 5,
			},
		},
	}
	handler := NewHandler(HandlerDeps{
		Provider:      prov,
		Model:         provider.ResolvedModel{BackendModelID: "advisor-model"},
		Config:        Config{MaxUsesPerRun: 1},
		UsageRecorder: nil,
	})

	ctx := contextWithSnapshot([]provider.Message{{Role: provider.MessageRoleUser, Content: "fix it"}})
	got, err := handler(ctx, nil)
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if got != "advice" {
		t.Fatalf("handler() = %#v, want %q", got, "advice")
	}
}

func TestHandlerDoesNotRecordFabricatedUsageWhenProviderReportsNone(t *testing.T) {
	t.Parallel()

	prov := &fakeProvider{
		response: provider.ChatResponse{
			Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "advice"},
			Usage:   nil,
		},
	}
	rec := usagestats.New(nil)
	const backendModelID = "advisor-no-usage-test-model"
	handler := NewHandler(HandlerDeps{
		Provider:      prov,
		Model:         provider.ResolvedModel{BackendModelID: backendModelID},
		Config:        Config{MaxUsesPerRun: 1},
		UsageRecorder: rec,
	})

	ctx := contextWithSnapshot([]provider.Message{{Role: provider.MessageRoleUser, Content: "fix it"}})
	if _, err := handler(ctx, nil); err != nil {
		t.Fatalf("handler() error = %v", err)
	}

	report := rec.Window(time.Hour)
	for _, row := range report.Rows {
		if row.BackendModelID == backendModelID {
			t.Fatalf("unexpected recorded row for %q: %#v (no usage means no observation recorded)", backendModelID, row)
		}
	}
}
