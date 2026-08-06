package advisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

type toolSink struct {
	events []output.Event
}

func (s *toolSink) Emit(event output.Event) {
	s.events = append(s.events, event)
}

func TestToolDefMinimalSchema(t *testing.T) {
	t.Parallel()

	def := ToolDef(nil)
	if got, want := def.Name, ToolName; got != want {
		t.Fatalf("Name = %q, want %q", got, want)
	}
	if def.Handler != nil {
		t.Fatal("Handler unexpectedly set")
	}
	if got := def.ParameterSchema["type"]; got != "object" {
		t.Fatalf("schema type = %v, want object", got)
	}
	if got := def.ParameterSchema["properties"]; got == nil {
		t.Fatal("schema properties = nil, want empty map")
	}
	if got := def.ParameterSchema["additionalProperties"]; got != false {
		t.Fatalf("schema additionalProperties = %v, want false", got)
	}
}

func TestNewHandlerUsesLiveConversationSnapshotAndEmitsEvents(t *testing.T) {
	t.Parallel()

	prov := &fakeProvider{
		response: provider.ChatResponse{
			Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "Ask for the failing test name before editing."},
		},
	}
	sink := &toolSink{}
	handler := NewHandler(HandlerDeps{
		Provider: prov,
		Model:    provider.ResolvedModel{BackendModelID: "advisor-model"},
		Events:   sink,
		Config:   Config{MaxUsesPerRun: 2, MaxTokens: intPtr(128)},
	})

	ctx := agent.WithConversationSnapshot(context.Background(), []provider.Message{
		{Role: provider.MessageRoleUser, Content: "fix it"},
	})
	got, err := handler(ctx, map[string]any{"ignored": true})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if got != "Ask for the failing test name before editing." {
		t.Fatalf("handler() = %#v, want advisor note", got)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(prov.requests))
	}
	if len(sink.events) != 2 {
		t.Fatalf("events = %d, want 2", len(sink.events))
	}

	started, ok := sink.events[0].Payload.(output.AdvisorStartedEvent)
	if !ok {
		t.Fatalf("event[0] payload = %T, want AdvisorStartedEvent", sink.events[0].Payload)
	}
	if started.UseNumber != 1 || started.MaxUses != 2 {
		t.Fatalf("started payload = %#v, want use 1/2", started)
	}

	completed, ok := sink.events[1].Payload.(output.AdvisorCompleteEvent)
	if !ok {
		t.Fatalf("event[1] payload = %T, want AdvisorCompleteEvent", sink.events[1].Payload)
	}
	if completed.UseNumber != 1 || completed.MaxUses != 2 {
		t.Fatalf("completed payload = %#v, want use 1/2", completed)
	}
	if completed.Note != "Ask for the failing test name before editing." {
		t.Fatalf("completed note = %q, want advisor note", completed.Note)
	}
}

func TestNewHandlerStopsAtBudgetWithoutCallingProvider(t *testing.T) {
	t.Parallel()

	prov := &fakeProvider{
		response: provider.ChatResponse{
			Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "first"},
		},
	}
	sink := &toolSink{}
	handler := NewHandler(HandlerDeps{
		Provider: prov,
		Model:    provider.ResolvedModel{BackendModelID: "advisor-model"},
		Events:   sink,
		Config:   Config{MaxUsesPerRun: 1},
	})

	ctx := agent.WithConversationSnapshot(context.Background(), []provider.Message{
		{Role: provider.MessageRoleUser, Content: "fix it"},
	})

	if _, err := handler(ctx, nil); err != nil {
		t.Fatalf("first handler() error = %v", err)
	}
	got, err := handler(ctx, nil)
	if err != nil {
		t.Fatalf("second handler() error = %v", err)
	}

	want := BudgetExhaustedMessage(1, 1)
	if got != want {
		t.Fatalf("second handler() = %#v, want %q", got, want)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(prov.requests))
	}
	if len(sink.events) != 3 {
		t.Fatalf("events = %d, want 3", len(sink.events))
	}
	budget, ok := sink.events[2].Payload.(output.AdvisorBudgetExhaustedEvent)
	if !ok {
		t.Fatalf("event[2] payload = %T, want AdvisorBudgetExhaustedEvent", sink.events[2].Payload)
	}
	if budget.Message != want {
		t.Fatalf("budget message = %q, want %q", budget.Message, want)
	}
}

func TestNewHandlerRequiresConversationSnapshot(t *testing.T) {
	t.Parallel()

	handler := NewHandler(HandlerDeps{
		Provider: &fakeProvider{},
		Model:    provider.ResolvedModel{BackendModelID: "advisor-model"},
		Config:   Config{MaxUsesPerRun: 1},
	})

	_, err := handler(context.Background(), nil)
	if err == nil {
		t.Fatal("handler() error = nil, want error")
	}
	if got := err.Error(); got != "advisor: live conversation snapshot missing from context" {
		t.Fatalf("handler() error = %q, want missing snapshot error", got)
	}
}

func TestNewHandlerEmitsCompleteEventOnProviderError(t *testing.T) {
	t.Parallel()

	sink := &toolSink{}
	handler := NewHandler(HandlerDeps{
		Provider: &fakeProvider{err: errors.New("backend failed")},
		Model:    provider.ResolvedModel{BackendModelID: "advisor-model"},
		Events:   sink,
		Config:   Config{MaxUsesPerRun: 1},
	})
	ctx := agent.WithConversationSnapshot(context.Background(), []provider.Message{
		{Role: provider.MessageRoleUser, Content: "fix it"},
	})

	_, err := handler(ctx, nil)
	if err == nil {
		t.Fatal("handler() error = nil, want wrapped error")
	}
	if len(sink.events) != 2 {
		t.Fatalf("events = %d, want 2", len(sink.events))
	}
	completed, ok := sink.events[1].Payload.(output.AdvisorCompleteEvent)
	if !ok {
		t.Fatalf("event[1] payload = %T, want AdvisorCompleteEvent", sink.events[1].Payload)
	}
	if completed.Error == "" {
		t.Fatal("completed error = empty, want provider error")
	}
}

func TestNewHandlerAppendsMarkerOnMaxTokensTruncation(t *testing.T) {
	t.Parallel()

	prov := &fakeProvider{
		response: provider.ChatResponse{
			Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "partial advice"},
			FinishReason: "length",
		},
	}
	sink := &toolSink{}
	handler := NewHandler(HandlerDeps{
		Provider: prov,
		Model:    provider.ResolvedModel{BackendModelID: "advisor-model"},
		Events:   sink,
		Config:   Config{MaxUsesPerRun: 1, MaxTokens: intPtr(10)},
	})

	ctx := agent.WithConversationSnapshot(context.Background(), []provider.Message{
		{Role: provider.MessageRoleUser, Content: "review this"},
	})
	got, err := handler(ctx, nil)
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("handler() = %T, want string", got)
	}
	if !strings.Contains(gotStr, "partial advice") {
		t.Fatalf("result missing original note: %q", gotStr)
	}
	if !strings.Contains(gotStr, "[advisor response truncated") {
		t.Fatalf("result missing truncation marker: %q", gotStr)
	}

	completed, ok := sink.events[1].Payload.(output.AdvisorCompleteEvent)
	if !ok {
		t.Fatalf("event[1] payload = %T, want AdvisorCompleteEvent", sink.events[1].Payload)
	}
	if !completed.Truncated {
		t.Fatal("completed.Truncated = false, want true")
	}
}

func TestToolDefSchemaExposesQuestionAndFiles(t *testing.T) {
	t.Parallel()

	def := ToolDef(nil)
	props, ok := def.ParameterSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %T, want map[string]any", def.ParameterSchema["properties"])
	}
	if _, ok := props["question"]; !ok {
		t.Fatal("schema missing question property")
	}
	if _, ok := props["files"]; !ok {
		t.Fatal("schema missing files property")
	}
	required, hasRequired := def.ParameterSchema["required"]
	if hasRequired {
		t.Fatalf("required = %#v, want no required fields", required)
	}
}

func newAdvisorTestPolicy(t *testing.T) (string, *tool.PathPolicy) {
	t.Helper()
	workDir := t.TempDir()
	policy := tool.NewPathPolicy(workDir, config.PathsConfig{})
	return workDir, &policy
}

func TestHandlerRejectsTooManyFilesWithoutConsumingUse(t *testing.T) {
	t.Parallel()

	workDir, policy := newAdvisorTestPolicy(t)
	prov := &fakeProvider{
		response: provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "note"}},
	}
	handler := NewHandler(HandlerDeps{
		Provider:   prov,
		Model:      provider.ResolvedModel{BackendModelID: "advisor-model"},
		Config:     Config{MaxUsesPerRun: 2},
		WorkDir:    workDir,
		PathPolicy: policy,
	})
	ctx := agent.WithConversationSnapshot(context.Background(), []provider.Message{
		{Role: provider.MessageRoleUser, Content: "fix it"},
	})

	paths := make([]any, maxAdvisorFiles+1)
	for i := range paths {
		paths[i] = "f.md"
	}

	_, err := handler(ctx, map[string]any{"files": paths})
	if err == nil {
		t.Fatal("handler() error = nil, want too-many-files error")
	}

	got, err := handler(ctx, nil)
	if err != nil {
		t.Fatalf("follow-up handler() error = %v", err)
	}
	if got != "note" {
		t.Fatalf("handler() = %#v, want first successful call to still be use 1", got)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1 (rejected call must not reach the provider)", len(prov.requests))
	}
}

func TestHandlerRejectsMissingFileWithoutConsumingUse(t *testing.T) {
	t.Parallel()

	workDir, policy := newAdvisorTestPolicy(t)
	prov := &fakeProvider{
		response: provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "note"}},
	}
	handler := NewHandler(HandlerDeps{
		Provider:   prov,
		Model:      provider.ResolvedModel{BackendModelID: "advisor-model"},
		Config:     Config{MaxUsesPerRun: 1},
		WorkDir:    workDir,
		PathPolicy: policy,
	})
	ctx := agent.WithConversationSnapshot(context.Background(), []provider.Message{
		{Role: provider.MessageRoleUser, Content: "fix it"},
	})

	_, err := handler(ctx, map[string]any{"files": []any{"missing.md"}})
	if err == nil {
		t.Fatal("handler() error = nil, want missing-file error")
	}

	// A malformed call must not have consumed the sole allotted use.
	got, err := handler(ctx, nil)
	if err != nil {
		t.Fatalf("follow-up handler() error = %v", err)
	}
	if got != "note" {
		t.Fatalf("handler() = %#v, want the use budget still available", got)
	}
}

func TestHandlerRejectsBlockedPathWithoutConsumingUse(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	blockedDir := filepath.Join(workDir, "secret")
	if err := os.Mkdir(blockedDir, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedDir, "value.txt"), []byte("shh"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	policy := tool.NewPathPolicy(workDir, config.PathsConfig{BlockedPaths: []string{"secret"}})

	prov := &fakeProvider{
		response: provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "note"}},
	}
	handler := NewHandler(HandlerDeps{
		Provider:   prov,
		Model:      provider.ResolvedModel{BackendModelID: "advisor-model"},
		Config:     Config{MaxUsesPerRun: 1},
		WorkDir:    workDir,
		PathPolicy: &policy,
	})
	ctx := agent.WithConversationSnapshot(context.Background(), []provider.Message{
		{Role: provider.MessageRoleUser, Content: "fix it"},
	})

	_, err := handler(ctx, map[string]any{"files": []any{"secret/value.txt"}})
	if err == nil {
		t.Fatal("handler() error = nil, want blocked-path error")
	}

	got, err := handler(ctx, nil)
	if err != nil {
		t.Fatalf("follow-up handler() error = %v", err)
	}
	if got != "note" {
		t.Fatalf("handler() = %#v, want the use budget still available", got)
	}
}

func TestHandlerValidCallWithFilesIncrementsUseAndReachesProvider(t *testing.T) {
	t.Parallel()

	workDir, policy := newAdvisorTestPolicy(t)
	if err := os.WriteFile(filepath.Join(workDir, "plan.yaml"), []byte("steps: []\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	prov := &fakeProvider{
		response: provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "note"}},
	}
	sink := &toolSink{}
	handler := NewHandler(HandlerDeps{
		Provider:   prov,
		Model:      provider.ResolvedModel{BackendModelID: "advisor-model"},
		Events:     sink,
		Config:     Config{MaxUsesPerRun: 1},
		WorkDir:    workDir,
		PathPolicy: policy,
	})
	ctx := agent.WithConversationSnapshot(context.Background(), []provider.Message{
		{Role: provider.MessageRoleUser, Content: "fix it"},
	})

	got, err := handler(ctx, map[string]any{"files": []any{"plan.yaml"}, "question": "is this sound?"})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if got != "note" {
		t.Fatalf("handler() = %#v, want advisor note", got)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(prov.requests))
	}
	last := prov.requests[0].Messages[len(prov.requests[0].Messages)-1]
	if !strings.Contains(last.Content, "steps: []") {
		t.Fatalf("provider request missing file content: %q", last.Content)
	}
	if !strings.Contains(last.Content, "is this sound?") {
		t.Fatalf("provider request missing question: %q", last.Content)
	}

	// The budget is now exhausted; a second call must not reach the provider.
	second, err := handler(ctx, nil)
	if err != nil {
		t.Fatalf("second handler() error = %v", err)
	}
	if second == "note" {
		t.Fatal("second handler() reached the provider, want budget-exhausted message")
	}
	if len(prov.requests) != 1 {
		t.Fatalf("provider calls after exhaustion = %d, want 1", len(prov.requests))
	}
}

func TestNewHandlerReturnsExplicitMessageOnEmptyContent(t *testing.T) {
	t.Parallel()

	prov := &fakeProvider{
		response: provider.ChatResponse{
			Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "  "},
			FinishReason: "stop",
		},
	}
	sink := &toolSink{}
	handler := NewHandler(HandlerDeps{
		Provider: prov,
		Model:    provider.ResolvedModel{BackendModelID: "advisor-model"},
		Events:   sink,
		Config:   Config{MaxUsesPerRun: 1},
	})

	ctx := agent.WithConversationSnapshot(context.Background(), []provider.Message{
		{Role: provider.MessageRoleUser, Content: "review this"},
	})
	got, err := handler(ctx, nil)
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("handler() = %T, want string", got)
	}
	if gotStr != "advisor returned no content" {
		t.Fatalf("handler() = %q, want explicit empty-content message", gotStr)
	}

	completed, ok := sink.events[1].Payload.(output.AdvisorCompleteEvent)
	if !ok {
		t.Fatalf("event[1] payload = %T, want AdvisorCompleteEvent", sink.events[1].Payload)
	}
	if completed.Note != "advisor returned no content" {
		t.Fatalf("completed.Note = %q, want explicit empty-content message", completed.Note)
	}
}
