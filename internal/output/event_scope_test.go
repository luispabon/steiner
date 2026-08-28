package output

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestWithAgentScope(t *testing.T) {
	base := Event{
		Type:      "test_event",
		Timestamp: time.Unix(123, 0).UTC(),
		Payload:   map[string]any{"message": "hello"},
	}
	baseScoped := base
	baseScoped.Scope.AgentID = "parent-child"

	tests := []struct {
		name    string
		event   Event
		agentID string
		want    Event
	}{
		{
			name:    "empty agent id returns original event",
			event:   base,
			agentID: "",
			want:    base,
		},
		{
			name:    "non-empty agent id sets scope",
			event:   base,
			agentID: "child-1",
			want: func() Event {
				event := base
				event.Scope.AgentID = "child-1"
				return event
			}(),
		},
		{
			name:    "empty agent id preserves existing scope",
			event:   baseScoped,
			agentID: "",
			want:    baseScoped,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WithAgentScope(tt.event, tt.agentID)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("WithAgentScope() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestWithAgentTypeScope(t *testing.T) {
	base := Event{Type: "test_event"}
	if got := WithAgentTypeScope(base, "code"); got.Scope.AgentType != "code" {
		t.Fatalf("Scope.AgentType = %q, want %q", got.Scope.AgentType, "code")
	}
	if got := WithAgentTypeScope(base, ""); got != base {
		t.Fatalf("empty agent type changed event: %#v", got)
	}
}

func TestWithAPIRequestKind(t *testing.T) {
	request := NewAPIRequestEvent("model", nil, nil, nil, nil, prompt.ModelTokenBudget{}, 0)
	got := WithAPIRequestKind(request, APIRequestKindCompaction)
	if payload := got.Payload.(APIRequestEvent); payload.Kind != APIRequestKindCompaction {
		t.Fatalf("API request Kind = %q, want %q", payload.Kind, APIRequestKindCompaction)
	}

	nonRequest := Event{Type: "test_event", Payload: map[string]any{"kind": "original"}}
	if got := WithAPIRequestKind(nonRequest, APIRequestKindCompaction); !reflect.DeepEqual(got, nonRequest) {
		t.Fatalf("non-API request event changed: %#v", got)
	}
}

func TestEventMarshalJSONScopes(t *testing.T) {
	base := Event{
		Type:      "test_event",
		Timestamp: time.Unix(123, 0).UTC(),
		Payload:   map[string]any{"message": "hello"},
	}
	scoped := WithAgentScope(base, "child-1")

	tests := []struct {
		name           string
		event          Event
		wantContains   string
		wantNotContain string
		wantScope      string
	}{
		{
			name:           "empty scope omitted",
			event:          base,
			wantNotContain: `"scope"`,
		},
		{
			name:         "agent scope included",
			event:        scoped,
			wantContains: `"scope":{"agent_id":"child-1"}`,
			wantScope:    "child-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.event)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			got := string(data)
			if tt.wantContains != "" && !strings.Contains(got, tt.wantContains) {
				t.Fatalf("json = %s, want to contain %s", got, tt.wantContains)
			}
			if tt.wantNotContain != "" && strings.Contains(got, tt.wantNotContain) {
				t.Fatalf("json = %s, want not to contain %s", got, tt.wantNotContain)
			}

			var roundTrip Event
			if err := json.Unmarshal(data, &roundTrip); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if got := roundTrip.Scope.AgentID; got != tt.wantScope {
				t.Fatalf("roundTrip.Scope.AgentID = %q, want %q", got, tt.wantScope)
			}
		})
	}
}

func TestEventMarshalJSONAgentTypeScope(t *testing.T) {
	base := Event{Type: "test_event", Timestamp: time.Unix(123, 0).UTC()}
	data, err := json.Marshal(WithAgentTypeScope(base, "code"))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if got := string(data); !strings.Contains(got, `"scope":{"agent_type":"code"}`) {
		t.Fatalf("json = %s, want agent type scope", got)
	}
	var roundTrip Event
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if roundTrip.Scope.AgentType != "code" {
		t.Fatalf("roundTrip.Scope.AgentType = %q, want %q", roundTrip.Scope.AgentType, "code")
	}
}

func TestNewModelCallFinishedEventTimingFields(t *testing.T) {
	cases := []struct {
		name       string
		durationMs int64
		ttftMs     int64
		outputTPS  float64
	}{
		{"zeros", 0, 0, 0},
		{"values", 1200, 340, 42.1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := NewModelCallFinishedEvent(ModelCallFinishedParams{
				Turn:             1,
				Model:            "model",
				FinishReason:     "stop",
				CompletionTokens: 100,
				DurationMs:       tc.durationMs,
				TTFTMs:           tc.ttftMs,
				OutputTPS:        tc.outputTPS,
			})
			payload := ev.Payload.(ModelCallFinishedEvent)
			if payload.DurationMs != tc.durationMs {
				t.Errorf("DurationMs: got %d, want %d", payload.DurationMs, tc.durationMs)
			}
			if payload.TTFTMs != tc.ttftMs {
				t.Errorf("TTFTMs: got %d, want %d", payload.TTFTMs, tc.ttftMs)
			}
			if payload.OutputTPS != tc.outputTPS {
				t.Errorf("OutputTPS: got %f, want %f", payload.OutputTPS, tc.outputTPS)
			}
		})
	}
}

func TestNewAPIRequestEventDeepClonesProviderOwnedFields(t *testing.T) {
	maxTokens := 64
	messages := []provider.Message{
		{
			Role: provider.MessageRoleAssistant,
			ToolCalls: []provider.ToolCall{
				{
					ID:        "call-1",
					Name:      "read",
					Arguments: map[string]any{"path": "a.go"},
				},
			},
			ProviderMetadata: &provider.MessageProviderMetadata{
				Anthropic: &provider.AnthropicMessageMetadata{ThinkingSignature: "sig"},
			},
		},
	}
	tools := []provider.ToolSpec{
		{
			Type: "function",
			Function: provider.ToolFunctionSpec{
				Name:       "read",
				Parameters: map[string]any{"type": "object"},
			},
		},
	}

	event := NewAPIRequestEvent("model", messages, tools, &maxTokens, nil, prompt.ModelTokenBudget{}, 42)
	payload := event.Payload.(APIRequestEvent)
	if payload.EstimatedPromptTokens != 42 {
		t.Fatalf("estimated prompt tokens = %d, want 42", payload.EstimatedPromptTokens)
	}
	payload.Messages[0].ToolCalls[0].Arguments["path"] = "b.go"
	payload.Messages[0].ProviderMetadata.Anthropic.ThinkingSignature = "changed"
	payload.Tools[0].Function.Parameters["type"] = "array"

	if got, want := messages[0].ToolCalls[0].Arguments["path"], "a.go"; got != want {
		t.Fatalf("original path = %v, want %v", got, want)
	}
	if got, want := messages[0].ProviderMetadata.Anthropic.ThinkingSignature, "sig"; got != want {
		t.Fatalf("original signature = %q, want %q", got, want)
	}
	if got, want := tools[0].Function.Parameters["type"], "object"; got != want {
		t.Fatalf("original tool parameter = %v, want %v", got, want)
	}
}

func TestAdvisorEventsRender(t *testing.T) {
	start := renderEvent(NewAdvisorStartedEvent("advisor-model", 1, 2, "", nil))
	if got := start.Text; !strings.Contains(got, "advisor started") || !strings.Contains(got, "use=1/2") {
		t.Fatalf("started text = %q, want advisor lifecycle summary", got)
	}

	complete := renderEvent(NewAdvisorCompleteEvent(AdvisorCompleteParams{Model: "advisor-model", UseNumber: 1, MaxUses: 2, Note: "check tests first"}))
	if got := complete.Text; !strings.Contains(got, "advisor complete") || !strings.Contains(got, "note=check tests first") {
		t.Fatalf("complete text = %q, want advisor note summary", got)
	}
	if got := complete.Text; strings.Contains(got, "cache=") {
		t.Fatalf("complete text = %q, want no cache= field when there is no cache-bearing usage", got)
	}

	completeWithUsage := NewAdvisorCompleteEvent(AdvisorCompleteParams{Model: "advisor-model", UseNumber: 1, MaxUses: 2, Note: "check tests first", CacheReadTokens: 10, CacheCreateTokens: 20, InputTokens: 30})
	payload, ok := completeWithUsage.Payload.(AdvisorCompleteEvent)
	if !ok {
		t.Fatalf("payload = %T, want AdvisorCompleteEvent", completeWithUsage.Payload)
	}
	if payload.CacheReadTokens != 10 || payload.CacheCreateTokens != 20 || payload.InputTokens != 30 {
		t.Fatalf("payload usage = %+v, want CacheReadTokens=10 CacheCreateTokens=20 InputTokens=30", payload)
	}
	completeWithUsageRendered := renderEvent(completeWithUsage)
	wantCacheField := "cache=16.7%" // 10 / (30+10+20) = 16.666...%
	if got := completeWithUsageRendered.Text; !strings.Contains(got, wantCacheField) {
		t.Fatalf("complete-with-usage text = %q, want %q", got, wantCacheField)
	}

	exhausted := renderEvent(NewAdvisorBudgetExhaustedEvent("advisor-model", 2, 2, "advisor budget exhausted for this session (2/2); proceed on your own judgment", "", nil))
	if got := exhausted.Text; !strings.Contains(got, "advisor budget exhausted") || !strings.Contains(got, "use=2/2") {
		t.Fatalf("exhausted text = %q, want advisor budget summary", got)
	}
}

// TestAdvisorCompleteEventCacheHitRateNotDoubleCounted pins Fix 0 (issue
// #490): InputTokens must already be the non-cached portion of the prompt, so
// for a turn with 100 total prompt tokens where 50 were served from cache,
// the rendered hit rate must be 50/100 = 50.0%, not 50/150.
func TestAdvisorCompleteEventCacheHitRateNotDoubleCounted(t *testing.T) {
	rendered := renderEvent(NewAdvisorCompleteEvent(AdvisorCompleteParams{Model: "advisor-model", UseNumber: 1, MaxUses: 2, Note: "note", CacheReadTokens: 50, InputTokens: 50}))
	if got := rendered.Text; !strings.Contains(got, "cache=50.0%") {
		t.Fatalf("text = %q, want cache=50.0%%", got)
	}
}

func TestDelegationCompleteEventRendersCacheHitRate(t *testing.T) {
	noUsage := renderEvent(NewDelegationCompleteEvent(DelegationCompleteParams{
		AgentID:       "child-1",
		Status:        "complete",
		TurnCount:     2,
		TokenCount:    100,
		ToolCallCount: 3,
	}))
	if got := noUsage.Text; strings.Contains(got, "cache=") {
		t.Fatalf("no-usage text = %q, want no cache= field when there is no cache-bearing usage", got)
	}

	withUsage := renderEvent(NewDelegationCompleteEvent(DelegationCompleteParams{
		AgentID:           "child-2",
		Status:            "complete",
		TurnCount:         4,
		TokenCount:        8123,
		ToolCallCount:     12,
		InputTokens:       50,
		CacheReadTokens:   950,
		CacheCreateTokens: 0,
	}))
	if got := withUsage.Text; !strings.Contains(got, "cache=95.0%") {
		t.Fatalf("with-usage text = %q, want cache=95.0%%", got)
	}
	for _, field := range []string{"tokens=8123", "tokens_in=1000", "tokens_out=8123", "tokens_total=9123"} {
		if !strings.Contains(withUsage.Text, field) {
			t.Fatalf("with-usage text = %q, missing %s", withUsage.Text, field)
		}
	}
}

// TestDelegationCompleteEventCacheHitRateNotDoubleCounted pins Fix 0 (issue
// #490): InputTokens must already be the non-cached portion of the prompt, so
// for a turn with 100 total prompt tokens where 50 were served from cache,
// the rendered hit rate must be 50/100 = 50.0%, not 50/150.
func TestDelegationCompleteEventCacheHitRateNotDoubleCounted(t *testing.T) {
	rendered := renderEvent(NewDelegationCompleteEvent(DelegationCompleteParams{
		AgentID:           "child-3",
		Status:            "complete",
		InputTokens:       50,
		CacheReadTokens:   50,
		CacheCreateTokens: 0,
	}))
	if got := rendered.Text; !strings.Contains(got, "cache=50.0%") {
		t.Fatalf("text = %q, want cache=50.0%%", got)
	}
}

func TestAdvisorStartedEventCarriesQuestionAndFilesInPayloadOnly(t *testing.T) {
	question := "check the layout"
	files := []string{"internal/tui/content_render_delegation.go", "internal/tui/delegation_layout.go"}
	event := NewAdvisorStartedEvent("advisor-model", 1, 2, question, files)

	payload, ok := event.Payload.(AdvisorStartedEvent)
	if !ok {
		t.Fatalf("payload type = %T, want AdvisorStartedEvent", event.Payload)
	}
	if payload.Question != question {
		t.Fatalf("payload.Question = %q, want %q", payload.Question, question)
	}
	if !reflect.DeepEqual(payload.Files, files) {
		t.Fatalf("payload.Files = %v, want %v", payload.Files, files)
	}

	// The new fields are payload-only: non-TUI text renderers must not surface them.
	rendered := renderEvent(event)
	if strings.Contains(rendered.Text, question) {
		t.Fatalf("rendered text = %q, contains question; want payload-only", rendered.Text)
	}
	for _, path := range files {
		if strings.Contains(rendered.Text, path) {
			t.Fatalf("rendered text = %q, contains file path %q; want payload-only", rendered.Text, path)
		}
	}
}

func TestPhaseEventsRender(t *testing.T) {
	transition := renderEvent(NewPhaseTransitionEvent("run-1", "plan", "implement", "starting", "plan-model", "session-1"))
	if got := transition.Text; !strings.Contains(got, "phase transition") || !strings.Contains(got, "plan -> implement") || !strings.Contains(got, "session=session-1") {
		t.Fatalf("transition text = %q, want phase transition summary", got)
	}

	indicator := renderEvent(NewPhaseIndicatorEvent("run-1", "plan", "running", "phase starting"))
	if got := indicator.Text; !strings.Contains(got, "phase plan running") || !strings.Contains(got, "phase starting") {
		t.Fatalf("indicator text = %q, want phase indicator summary", got)
	}
}
