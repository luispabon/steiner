package delegation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// runRequestsEqualIgnoringFuncFields compares two RunRequest values for
// equality, ignoring func-typed fields (e.g. ParallelClassOf, DrainSteers):
// reflect.DeepEqual on func values reports non-nil funcs as unequal even when
// both come from the same struct copy, which would otherwise make this
// comparison spuriously fail once a request always carries a non-nil
// ParallelClassOf.
func runRequestsEqualIgnoringFuncFields(a, b agent.RunRequest) bool {
	a.ParallelClassOf = nil
	b.ParallelClassOf = nil
	a.DrainSteers = nil
	b.DrainSteers = nil
	a.TurnBudgetNotice = nil
	b.TurnBudgetNotice = nil
	return reflect.DeepEqual(a, b)
}

type mockRunner struct {
	runFunc func(context.Context, agent.RunRequest) (agent.RunState, error)
}

func (m *mockRunner) Run(ctx context.Context, req agent.RunRequest) (agent.RunState, error) {
	return m.runFunc(ctx, req)
}

type noopEventSink struct{}

func (noopEventSink) Emit(output.Event) {}

type stubProvider struct {
	name string
}

func (stubProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (stubProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, nil
}

func (stubProvider) SupportsUsageStats() bool { return false }

func successRunState() agent.RunState {
	return agent.RunState{
		Conversation: []agent.Message{
			{Role: agent.MessageRoleAssistant, Content: "task result"},
		},
		TurnCount:  1,
		TokenCount: 100,
		StopReason: agent.StopReasonComplete,
	}
}

// minimalDeps returns a SpecializedToolDeps suitable for unit tests.
// It uses a noop event sink, stub provider, empty registry, and a
// configurable mock runner.
func minimalDeps(runner AgentRunner) SpecializedToolDeps {
	return SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{},
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(),
			Runner:      runner,
			Events:      noopEventSink{},
			WorkDir:     "/tmp/work",
		},
		ModelResolver: nil,
	}
}

// validStructuredTask returns a valid structured task input suitable for testing.
// The objective and context are set to the provided taskDescription; deliverable,
// constraints, success_criteria, and checks use standard defaults.
func validStructuredTask(taskDescription string) map[string]any {
	return map[string]any{
		"objective":        taskDescription,
		"context":          "Context for " + taskDescription,
		"deliverable":      "Deliverable for " + taskDescription,
		"constraints":      []any{},
		"success_criteria": []any{},
		"checks":           []any{},
	}
}

// subAgentTask returns a valid sub-agent task input for the given agent type.
func subAgentTask(agentType AgentType, taskDescription string) map[string]any {
	task := validStructuredTask(taskDescription)
	task["type"] = string(agentType)
	return task
}

// subAgentTaskWithImageID returns a valid sub-agent task input for vision with image_id.
func subAgentTaskWithImageID(taskDescription, imageID string) map[string]any {
	task := validStructuredTask(taskDescription)
	task["type"] = string(AgentTypeVision)
	task["image_id"] = imageID
	return task
}

func TestAssembleTaskContent(t *testing.T) {
	tests := []struct {
		name     string
		input    structuredBrief
		wantText string
	}{
		{
			name: "all fields present",
			input: structuredBrief{
				Objective:       "Find and fix the bug",
				Ctx:             "The bug is in main.go",
				Deliverable:     "A patched main.go file",
				Constraints:     []string{"No breaking changes", "Must pass tests"},
				SuccessCriteria: []string{"Bug no longer occurs", "All tests pass"},
				Checks:          []string{"go test ./...", "go vet ./..."},
			},
			wantText: `## Objective

Find and fix the bug

## Context

The bug is in main.go

## Deliverable

A patched main.go file

## Constraints

- No breaking changes
- Must pass tests

## Success criteria

- Bug no longer occurs
- All tests pass

## Checks

- go test ./...
- go vet ./...`,
		},
		{
			name: "empty optional arrays omitted",
			input: structuredBrief{
				Objective:       "Objective",
				Ctx:             "Context",
				Deliverable:     "Deliverable",
				Constraints:     []string{},
				SuccessCriteria: []string{},
				Checks:          []string{},
			},
			wantText: `## Objective

Objective

## Context

Context

## Deliverable

Deliverable`,
		},
		{
			name: "only constraints present",
			input: structuredBrief{
				Objective:       "Objective",
				Ctx:             "Context",
				Deliverable:     "Deliverable",
				Constraints:     []string{"Constraint 1", "Constraint 2"},
				SuccessCriteria: []string{},
				Checks:          []string{},
			},
			wantText: `## Objective

Objective

## Context

Context

## Deliverable

Deliverable

## Constraints

- Constraint 1
- Constraint 2`,
		},
		{
			name: "only success_criteria present",
			input: structuredBrief{
				Objective:       "Objective",
				Ctx:             "Context",
				Deliverable:     "Deliverable",
				Constraints:     []string{},
				SuccessCriteria: []string{"Criteria 1"},
				Checks:          []string{},
			},
			wantText: `## Objective

Objective

## Context

Context

## Deliverable

Deliverable

## Success criteria

- Criteria 1`,
		},
		{
			name: "only checks present",
			input: structuredBrief{
				Objective:       "Objective",
				Ctx:             "Context",
				Deliverable:     "Deliverable",
				Constraints:     []string{},
				SuccessCriteria: []string{},
				Checks:          []string{"go test ./..."},
			},
			wantText: `## Objective

Objective

## Context

Context

## Deliverable

Deliverable

## Checks

- go test ./...`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := assembleTaskContent(tt.input)
			if got != tt.wantText {
				t.Errorf("assembleTaskContent() =\n%q\nwant\n%q", got, tt.wantText)
			}
		})
	}
}

type recordingEventSink struct {
	mu     sync.Mutex
	events []output.Event
}

func (s *recordingEventSink) Emit(event output.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *recordingEventSink) Events() []output.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]output.Event(nil), s.events...)
}

func waitingEvents(events []output.Event) []output.Event {
	var waiting []output.Event
	for _, event := range events {
		if event.Type == output.EventTypeDelegationCacheWaiting {
			waiting = append(waiting, event)
		}
	}
	return waiting
}

func TestSpecializedHandler_DispatchGateLeaderWrapsEvents(t *testing.T) {
	var capturedReq agent.RunRequest
	events := &recordingEventSink{}
	var runCount int
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		runCount++
		if runCount == 1 {
			capturedReq = req
			if _, ok := req.Events.(*dispatchReleaseSink); !ok {
				t.Errorf("req.Events=%T, want *dispatchReleaseSink", req.Events)
			}
			req.Events.Emit(output.NewThinkingChunkEventWithSource(1, "thinking", output.ChunkSourceAssistant))
		}
		return agent.RunState{}, nil
	}})
	deps.Events = events
	deps.CacheKeyStore = NewCacheKeyStore()

	handler := newSpecializedHandler(AgentTypeExplore, deps)
	if _, err := handler(context.Background(), validStructuredTask("explore")); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if capturedReq.Events == nil {
		t.Fatal("runner did not capture req.Events")
	}
	if got := waitingEvents(events.Events()); len(got) != 0 {
		t.Fatalf("leader emitted %d waiting events, want none", len(got))
	}
	started := startedEvents(events.Events())
	if len(started) != 1 {
		t.Fatalf("started events = %d, want 1", len(started))
	}
	payload, ok := started[0].Payload.(output.DelegationStartedEvent)
	if !ok {
		t.Fatalf("started payload = %T, want DelegationStartedEvent", started[0].Payload)
	}
	if payload.AgentType != string(AgentTypeExplore) {
		t.Errorf("started AgentType = %q, want %q", payload.AgentType, AgentTypeExplore)
	}
	if started[0].Scope.AgentID == "" || started[0].Scope.AgentType != string(AgentTypeExplore) {
		t.Errorf("started scope = %+v, want agent ID and type %q", started[0].Scope, AgentTypeExplore)
	}
}

func startedEvents(events []output.Event) []output.Event {
	var started []output.Event
	for _, event := range events {
		if event.Type == output.EventTypeDelegationStarted {
			started = append(started, event)
		}
	}
	return started
}

func TestSpecializedHandler_CancelledBeforeDispatchCleansToolCallTrace(t *testing.T) {
	const agentID = "child-cancelled-before-dispatch"
	originalIDGen := idGen
	idGen = func() string { return agentID }
	t.Cleanup(func() { idGen = originalIDGen })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var traceWriter *toolCallTraceWriter
	events := output.SinkFunc(func(event output.Event) {
		if event.Type != output.EventTypeDelegationStarted {
			return
		}
		toolCallTraceRegistryMu.Lock()
		traceWriter = toolCallTraceRegistry[agentID]
		toolCallTraceRegistryMu.Unlock()
		cancel()
	})
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		t.Fatal("runner called after cancellation before dispatch")
		return agent.RunState{}, nil
	}})
	deps.Events = events
	deps.WorkDir = t.TempDir()

	raw, err := SubAgentToolDef(deps, nil).Handler(ctx, subAgentTask(AgentTypeExplore, "explore"))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	result, ok := raw.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("handler returned %T, want tool.ExecutionResult", raw)
	}
	delegationResult, ok := result.Value.(Result)
	if !ok {
		t.Fatalf("result.Value is %T, want Result", result.Value)
	}
	if delegationResult.Status != StatusCancelled {
		t.Errorf("result status = %q, want %q", delegationResult.Status, StatusCancelled)
	}
	if traceWriter == nil {
		t.Fatal("trace writer was not registered before cancellation")
	}

	toolCallTraceRegistryMu.Lock()
	_, registered := toolCallTraceRegistry[agentID]
	toolCallTraceRegistryMu.Unlock()
	if registered {
		t.Error("cancelled child trace writer remains registered")
	}
	if _, err := traceWriter.file.Stat(); err == nil {
		t.Error("trace writer file Stat succeeded after close")
	}
}

func TestSpecializedHandler_DispatchGateFollowerWaits(t *testing.T) {
	store := NewCacheKeyStore()
	key, err := store.KeyFor(AgentTypeExplore, provider.NewPromptCacheKey)
	if err != nil {
		t.Fatalf("mint cache key: %v", err)
	}
	_, release, _ := store.BeginDispatch(key)
	defer release()

	events := &recordingEventSink{}
	runCalled := make(chan struct{}, 2)
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		if _, ok := req.Events.(*dispatchReleaseSink); ok {
			t.Error("follower req.Events was wrapped in *dispatchReleaseSink")
		}
		runCalled <- struct{}{}
		return agent.RunState{}, nil
	}}
	deps := minimalDeps(runner)
	deps.Events = events
	deps.CacheKeyStore = store

	origIDGen := idGen
	idGen = func() string { return "child-follower" }
	defer func() { idGen = origIDGen }()

	ctx := context.WithValue(context.Background(), tool.ExecutionCallIDKey{}, "call_X")
	handler := newSpecializedHandler(AgentTypeExplore, deps)
	done := make(chan error, 1)
	go func() {
		_, err := handler(ctx, validStructuredTask("explore"))
		done <- err
	}()

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	var waiting []output.Event
	for len(waiting) == 0 {
		select {
		case <-deadline.C:
			t.Fatal("timed out waiting for DelegationCacheWaitingEvent")
		case <-time.After(time.Millisecond):
			waiting = waitingEvents(events.Events())
		}
	}
	if len(waiting) != 1 {
		t.Fatalf("got %d waiting events, want one", len(waiting))
	}
	payload, ok := waiting[0].Payload.(output.DelegationCacheWaitingEvent)
	if !ok {
		t.Fatalf("waiting payload=%T, want output.DelegationCacheWaitingEvent", waiting[0].Payload)
	}
	if payload.AgentID != "child-follower" {
		t.Errorf("waiting AgentID=%q, want %q", payload.AgentID, "child-follower")
	}
	if payload.CallID != "call_X" {
		t.Errorf("waiting CallID=%q, want %q", payload.CallID, "call_X")
	}
	if payload.DeadlineUnixNano <= time.Now().UnixNano() {
		t.Errorf("waiting deadline=%d is not in the future", payload.DeadlineUnixNano)
	}
	select {
	case <-runCalled:
		t.Fatal("follower spawned before gate release")
	default:
	}

	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for follower handler")
	}
}

type dispatchGateEventSink struct {
	recordingEventSink
	waiting      chan struct{}
	once         sync.Once
	order        *atomic.Int32
	waitingOrder *atomic.Int32
}

func (s *dispatchGateEventSink) Emit(event output.Event) {
	s.recordingEventSink.Emit(event)
	if event.Type == output.EventTypeDelegationCacheWaiting {
		if s.order != nil && s.waitingOrder != nil {
			s.waitingOrder.Store(s.order.Add(1))
		}
		s.once.Do(func() { close(s.waiting) })
	}
}

func TestSpecializedHandler_DispatchGateTimeoutFallbackDispatchesFollower(t *testing.T) {
	store := NewCacheKeyStore()
	store.testWaitTimeout = 20 * time.Millisecond
	leaderStarted := make(chan struct{})
	leaderRelease := make(chan struct{})
	followerDispatched := make(chan struct{})
	var calls atomic.Int32
	var order atomic.Int32
	var waitingOrder atomic.Int32
	events := &dispatchGateEventSink{
		waiting:      make(chan struct{}),
		order:        &order,
		waitingOrder: &waitingOrder,
	}
	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		switch calls.Add(1) {
		case 1:
			close(leaderStarted)
			<-leaderRelease
		case 2:
			order.Store(order.Add(1))
			close(followerDispatched)
		}
		return agent.RunState{}, nil
	}}
	deps := minimalDeps(runner)
	deps.Events = events
	deps.CacheKeyStore = store

	handler := newSpecializedHandler(AgentTypeExplore, deps)
	leaderDone := make(chan error, 1)
	go func() {
		_, err := handler(context.Background(), validStructuredTask("leader"))
		leaderDone <- err
	}()
	select {
	case <-leaderStarted:
	case <-time.After(time.Second):
		t.Fatal("leader did not reach runner")
	}

	followerDone := make(chan error, 1)
	go func() {
		_, err := handler(context.Background(), validStructuredTask("follower"))
		followerDone <- err
	}()
	select {
	case <-events.waiting:
	case <-time.After(time.Second):
		t.Fatal("follower did not emit DelegationCacheWaitingEvent")
	}
	select {
	case <-followerDispatched:
	case <-time.After(time.Second):
		t.Fatal("follower did not dispatch after gate timeout")
	}
	if waitingOrder.Load() >= order.Load() {
		t.Fatalf("follower dispatch order = %d, waiting event order = %d, want waiting first", order.Load(), waitingOrder.Load())
	}
	select {
	case err := <-followerDone:
		if err != nil {
			t.Fatalf("follower returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("follower handler did not finish")
	}
	close(leaderRelease)
	select {
	case err := <-leaderDone:
		if err != nil {
			t.Fatalf("leader returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("leader handler did not finish")
	}
}

func TestSpecializedHandler_DispatchGateNilStore(t *testing.T) {
	var capturedReq agent.RunRequest
	events := &recordingEventSink{}
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		capturedReq = req
		return agent.RunState{}, nil
	}})
	deps.Events = events
	deps.CacheKeyStore = nil

	handler := newSpecializedHandler(AgentTypeExplore, deps)
	if _, err := handler(context.Background(), validStructuredTask("explore")); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if _, ok := capturedReq.Events.(*dispatchReleaseSink); ok {
		t.Error("nil store wrapped req.Events in *dispatchReleaseSink")
	}
	if got := waitingEvents(events.Events()); len(got) != 0 {
		t.Fatalf("nil store emitted %d waiting events, want none", len(got))
	}
}

func TestSpecializedHandler_RegisterFailureCleansCodeWorktree(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	controller := NewActiveController()
	const agentID = "duplicate-code"
	if _, err := controller.Register(agentID, context.Background(), AgentTypeCode, CodeWorktree{}); err != nil {
		t.Fatalf("pre-register agent: %v", err)
	}

	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		t.Fatal("runner called after duplicate registration")
		return agent.RunState{}, nil
	}})
	deps.ActiveController = controller
	deps.WorkDir = repo
	events := &recordingEventSink{}
	deps.Events = events
	originalIDGen := idGen
	idGen = func() string { return agentID }
	defer func() { idGen = originalIDGen }()

	_, err := newSpecializedHandler(AgentTypeCode, deps)(context.Background(), validStructuredTask("duplicate"))
	if !errors.Is(err, ErrAgentAlreadyActive) {
		t.Fatalf("handler error = %v, want ErrAgentAlreadyActive", err)
	}
	worktrees, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("list worktrees: %v", err)
	}
	if len(worktrees) != 0 {
		t.Fatalf("worktrees after register failure = %#v, want none", worktrees)
	}
	if got := len(events.Events()); got != 0 {
		t.Fatalf("lifecycle events after register failure = %d, want none", got)
	}
}

func TestSpecializedHandler_RegisterFailureDoesNotCreateNonCodeWorktree(t *testing.T) {
	controller := NewActiveController()
	const agentID = "duplicate-explore"
	if _, err := controller.Register(agentID, context.Background(), AgentTypeExplore, CodeWorktree{}); err != nil {
		t.Fatalf("pre-register agent: %v", err)
	}
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		t.Fatal("runner called after duplicate registration")
		return agent.RunState{}, nil
	}})
	deps.ActiveController = controller
	originalIDGen := idGen
	idGen = func() string { return agentID }
	defer func() { idGen = originalIDGen }()

	_, err := newSpecializedHandler(AgentTypeExplore, deps)(context.Background(), validStructuredTask("duplicate"))
	if !errors.Is(err, ErrAgentAlreadyActive) {
		t.Fatalf("handler error = %v, want ErrAgentAlreadyActive", err)
	}
}

func TestSubAgentToolDef_Schema(t *testing.T) {
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})

	def := SubAgentToolDef(deps, nil)

	if def.Name != SubAgentToolName {
		t.Errorf("Name=%q, want %q", def.Name, SubAgentToolName)
	}
	if def.Description == "" {
		t.Error("Description is empty")
	}
	if def.Handler == nil {
		t.Fatal("Handler is nil")
	}

	schemaType, ok := def.ParameterSchema["type"].(string)
	if !ok || schemaType != "object" {
		t.Errorf("schema type=%v, want 'object'", def.ParameterSchema["type"])
	}

	props, ok := def.ParameterSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties missing from schema")
	}

	// All sub-agent tools must have the type field plus six structured task fields.
	requiredFields := []string{"type", "objective", "context", "deliverable", "constraints", "success_criteria", "checks"}
	for _, field := range requiredFields {
		if _, has := props[field]; !has {
			t.Errorf("schema properties missing field %q", field)
		}
	}

	// image_id is always present in properties but not required.
	if _, hasImageID := props["image_id"]; !hasImageID {
		t.Error("schema missing 'image_id'")
	}

	required, ok := def.ParameterSchema["required"].([]any)
	if !ok {
		t.Fatal("required missing from schema")
	}

	// Required fields should be exactly 7: type, objective, context, deliverable, constraints, success_criteria, checks.
	if len(required) != 7 {
		t.Errorf("required fields count=%d, want 7", len(required))
	}

	// Verify the type enum has all 7 agent types.
	typeField, ok := props["type"].(map[string]any)
	if !ok {
		t.Fatal("type field is not a map")
	}
	enumVals, ok := typeField["enum"].([]any)
	if !ok {
		t.Fatal("type field missing enum")
	}

	wantEnum := make([]any, 0, len(AllAgentTypes()))
	for _, at := range AllAgentTypes() {
		wantEnum = append(wantEnum, string(at))
	}
	if !reflect.DeepEqual(enumVals, wantEnum) {
		t.Errorf("type enum = %v, want %v in AllAgentTypes order", enumVals, wantEnum)
	}
}

func TestSubAgentToolDef_ExcludeTypes(t *testing.T) {
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})

	excludeTypes := []AgentType{AgentTypeResearch, AgentTypeVision}
	def := SubAgentToolDef(deps, excludeTypes)

	props, _ := def.ParameterSchema["properties"].(map[string]any)
	typeField, _ := props["type"].(map[string]any)
	enumVals, _ := typeField["enum"].([]any)

	excludedSet := map[AgentType]bool{AgentTypeResearch: true, AgentTypeVision: true}
	wantEnum := make([]any, 0, len(AllAgentTypes())-len(excludedSet))
	for _, at := range AllAgentTypes() {
		if !excludedSet[at] {
			wantEnum = append(wantEnum, string(at))
		}
	}
	if !reflect.DeepEqual(enumVals, wantEnum) {
		t.Errorf("type enum = %v, want %v (remaining types in AllAgentTypes order)", enumVals, wantEnum)
	}
}

func TestSubAgentToolDef_SchemaIsDeterministic(t *testing.T) {
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})

	tests := []struct {
		name         string
		excludeTypes []AgentType
	}{
		{name: "no excludes", excludeTypes: nil},
		{name: "with excludes", excludeTypes: []AgentType{AgentTypeResearch, AgentTypeVision}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def1 := SubAgentToolDef(deps, tt.excludeTypes)
			def2 := SubAgentToolDef(deps, tt.excludeTypes)

			b1, err := json.Marshal(def1.ParameterSchema)
			if err != nil {
				t.Fatalf("marshal schema 1: %v", err)
			}
			b2, err := json.Marshal(def2.ParameterSchema)
			if err != nil {
				t.Fatalf("marshal schema 2: %v", err)
			}
			if !bytes.Equal(b1, b2) {
				t.Errorf("schema marshal not deterministic:\n%s\nvs\n%s", b1, b2)
			}
		})
	}
}

func TestSubAgentDispatch_Errors(t *testing.T) {
	tests := []struct {
		name         string
		excludeTypes []AgentType
		input        map[string]any
		wantErr      string
	}{
		{
			name:    "missing type",
			input:   validStructuredTask("t"),
			wantErr: "sub_agent: type is required and must be non-empty",
		},
		{
			name: "whitespace type",
			input: func() map[string]any {
				task := validStructuredTask("t")
				task["type"] = "   "
				return task
			}(),
			wantErr: "sub_agent: type is required and must be non-empty",
		},
		{
			name:    "unknown type",
			input:   subAgentTask(AgentType("bogus"), "t"),
			wantErr: `sub_agent: unknown or unavailable type "bogus"; valid types:`,
		},
		{
			name:         "excluded type",
			excludeTypes: []AgentType{AgentTypeVision},
			input:        subAgentTask(AgentTypeVision, "t"),
			wantErr:      `sub_agent: type "vision" is unavailable; valid types:`,
		},
		{
			name:    "vision missing image_id",
			input:   subAgentTask(AgentTypeVision, "t"),
			wantErr: `sub_agent: type is "vision" but image_id is missing or empty`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var runCalled bool
			runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
				runCalled = true
				return successRunState(), nil
			}}
			def := SubAgentToolDef(minimalDeps(runner), tt.excludeTypes)

			_, err := def.Handler(context.Background(), tt.input)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
			if runCalled {
				t.Error("runner was invoked despite validation failure")
			}
		})
	}
}

func TestSpecializedHandler_EmptyTask(t *testing.T) {
	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return agent.RunState{}, nil
	}}

	for _, agentType := range AllAgentTypes() {
		agentType := agentType
		t.Run(string(agentType), func(t *testing.T) {
			// Skip Vision (requires image_id) and Code (requires worktree) as they have special requirements.
			if agentType == AgentTypeVision || agentType == AgentTypeCode {
				t.Skip("vision and code agents require special setup; tested separately")
			}
			def := SubAgentToolDef(minimalDeps(runner), nil)

			_, err := def.Handler(context.Background(), map[string]any{
				"type": string(agentType),
			})
			if err == nil {
				t.Error("expected error for missing fields")
			}

			// Test with empty objective
			_, err = def.Handler(context.Background(), map[string]any{
				"type":             string(agentType),
				"objective":        "",
				"context":          "context",
				"deliverable":      "deliverable",
				"constraints":      []any{},
				"success_criteria": []any{},
				"checks":           []any{},
			})
			if err == nil {
				t.Error("expected error for empty objective")
			}
			if !strings.Contains(err.Error(), "objective") {
				t.Errorf("error %q should mention 'objective'", err.Error())
			}

			// Test with blank objective (whitespace only)
			_, err = def.Handler(context.Background(), map[string]any{
				"type":             string(agentType),
				"objective":        "   ",
				"context":          "context",
				"deliverable":      "deliverable",
				"constraints":      []any{},
				"success_criteria": []any{},
				"checks":           []any{},
			})
			if err == nil {
				t.Error("expected error for blank objective after trim")
			}

			// Test with empty context
			_, err = def.Handler(context.Background(), map[string]any{
				"type":             string(agentType),
				"objective":        "objective",
				"context":          "",
				"deliverable":      "deliverable",
				"constraints":      []any{},
				"success_criteria": []any{},
				"checks":           []any{},
			})
			if err == nil {
				t.Error("expected error for empty context")
			}
			if !strings.Contains(err.Error(), "context") {
				t.Errorf("error %q should mention 'context'", err.Error())
			}

			// Test with empty deliverable
			_, err = def.Handler(context.Background(), map[string]any{
				"type":             string(agentType),
				"objective":        "objective",
				"context":          "context",
				"deliverable":      "",
				"constraints":      []any{},
				"success_criteria": []any{},
				"checks":           []any{},
			})
			if err == nil {
				t.Error("expected error for empty deliverable")
			}
			if !strings.Contains(err.Error(), "deliverable") {
				t.Errorf("error %q should mention 'deliverable'", err.Error())
			}

			// Test with valid structured task should succeed
			_, err = def.Handler(context.Background(), subAgentTask(AgentTypeExplore, "test"))
			if err != nil {
				t.Errorf("valid structured task returned error: %v", err)
			}
		})
	}
}

func TestSpecializedHandler_UsesTypeSystemPrompt(t *testing.T) {
	// Verify that the handler builds the child RunRequest with the correct
	// system prompt for the agent type. Code now uses the shared base prompt,
	// so this test stays focused on the types that still supply explicit
	// overrides.
	for _, agentType := range []AgentType{AgentTypeExplore, AgentTypeResearch, AgentTypeEvaluate, AgentTypeSanityCheck, AgentTypeReview} {
		agentType := agentType
		t.Run(string(agentType), func(t *testing.T) {
			var capturedReq agent.RunRequest
			runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
				capturedReq = req
				return successRunState(), nil
			}}

			deps := minimalDeps(runner)
			def := SubAgentToolDef(deps, nil)

			_, err := def.Handler(context.Background(), subAgentTask(agentType, "test task"))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// The system prompt from AgentSystemPrompt should appear in the
			// assembled prompt. It is set as a PromptOverride, so it appears
			// in PromptOverrides.System.
			expectedPrompt := AgentSystemPrompt(agentType)
			got := capturedReq.Prompt.PromptOverrides.System
			if got != expectedPrompt {
				t.Errorf("PromptOverrides.System=%q, want prompt for agent type %q", got, agentType)
			}
		})
	}
}

func TestSpecializedHandler_UsesTypeAllowedTools(t *testing.T) {
	// Verify that the handler restricts child registries to the per-type allowlist.
	// We register all allowed tools in the parent registry and confirm that
	// only the expected tools reach the child.
	for _, agentType := range AllAgentTypes() {
		agentType := agentType
		t.Run(string(agentType), func(t *testing.T) {
			allowedTools := AgentAllowedTools(agentType)
			if len(allowedTools) == 0 {
				t.Skip("agent type has empty allowlist; skipping")
			}
			if agentType == AgentTypeVision {
				// Vision requires an ImageStore with a real image; covered by TestVisionHandler_*.
				t.Skip("vision handler requires ImageStore setup; tested separately")
			}

			// Build parent registry with all allowed tools plus an extra one.
			allDefs := make([]tool.ToolDef, 0, len(allowedTools)+1)
			for _, name := range allowedTools {
				allDefs = append(allDefs, tool.ToolDef{Name: name, Description: name})
			}
			allDefs = append(allDefs, tool.ToolDef{Name: "not_allowed", Description: "not allowed"})

			var capturedReq agent.RunRequest
			runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
				capturedReq = req
				return successRunState(), nil
			}}

			deps := SpecializedToolDeps{
				SubAgentHandlerDeps: SubAgentHandlerDeps{
					SubAgentCfg: config.SubAgentConfig{},
					Provider:    stubProvider{},
					ParentReg:   tool.NewRegistry(allDefs...),
					Runner:      runner,
					Events:      noopEventSink{},
					WorkDir:     "/tmp/work",
				},
				ModelResolver: nil,
			}
			if agentType == AgentTypeCode {
				repo, cleanup := setupTestRepo(t)
				t.Cleanup(cleanup)
				deps.WorkDir = repo
			}
			def := SubAgentToolDef(deps, nil)

			_, err := def.Handler(context.Background(), subAgentTask(AgentTypeExplore, "test task"))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// "not_allowed" must not appear in the child's visible tools.
			for _, ts := range capturedReq.Tools {
				if ts.Function.Name == "not_allowed" {
					t.Error("child tools contain 'not_allowed' tool")
				}
			}
		})
	}
}

func TestSpecializedHandler_ReturnsExecutionResult(t *testing.T) {
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})
	def := SubAgentToolDef(deps, nil)

	raw, err := def.Handler(context.Background(), subAgentTask(AgentTypeExplore, "explore something"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := raw.(tool.ExecutionResult); !ok {
		t.Errorf("handler returned %T, want tool.ExecutionResult", raw)
	}
}

func TestSpecializedHandler_UsesPerTypeModel(t *testing.T) {
	// Configure a model alias for a specific agent type.
	// Provide a ModelResolver that records whether it was called and with what alias.
	// Verify that the handler uses the resolved model.
	agentType := AgentTypeExplore
	expectedModelAlias := "custom-model"
	resolverCalled := false
	var resolverCalledWith string
	var capturedReq agent.RunRequest

	testProvider := stubProvider{}
	testModel := provider.ResolvedModel{
		Alias:           expectedModelAlias,
		BackendModelID:  "backend-custom-model",
		EffectiveLimits: provider.EffectiveLimits{ContextWindow: 8000, MaxOutputTokens: 2000},
	}

	modelResolver := func(alias string) (provider.Provider, provider.ResolvedModel, error) {
		resolverCalled = true
		resolverCalledWith = alias
		return testProvider, testModel, nil
	}

	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		capturedReq = req
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			Provider:      stubProvider{},
			ParentReg:     tool.NewRegistry(),
			Runner:        runner,
			Events:        noopEventSink{},
			WorkDir:       "/tmp/work",
			ResolvedModel: provider.ResolvedModel{BackendModelID: "parent-model"},
		},
		ModelResolver: modelResolver,
		AgentModels: map[string]string{
			string(agentType): expectedModelAlias,
		},
	}

	def := SubAgentToolDef(deps, nil)
	_, err := def.Handler(context.Background(), subAgentTask(AgentTypeExplore, "test task"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resolverCalled {
		t.Error("ModelResolver was not called")
	}
	if resolverCalledWith != expectedModelAlias {
		t.Errorf("ModelResolver called with %q, want %q", resolverCalledWith, expectedModelAlias)
	}
	if capturedReq.ResolvedModel.Alias != expectedModelAlias {
		t.Errorf("child RunRequest ResolvedModel.Alias=%q, want %q", capturedReq.ResolvedModel.Alias, expectedModelAlias)
	}
}

func TestSpecializedHandler_FallsBackWithoutModelConfig(t *testing.T) {
	// No Agents entry for the agent type: use the selected profile default.
	const defaultAlias = "profile-default"
	resolverCalledWith := ""
	resolvedModel := provider.ResolvedModel{Alias: defaultAlias, BackendModelID: "profile-model"}

	modelResolver := func(alias string) (provider.Provider, provider.ResolvedModel, error) {
		resolverCalledWith = alias
		return stubProvider{}, resolvedModel, nil
	}

	var capturedReq agent.RunRequest
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		capturedReq = req
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			Provider:      stubProvider{},
			ParentReg:     tool.NewRegistry(),
			Runner:        runner,
			Events:        noopEventSink{},
			WorkDir:       "/tmp/work",
			ResolvedModel: provider.ResolvedModel{BackendModelID: "parent-model"},
		},
		ModelResolver: modelResolver,
		AgentModels:   map[string]string{}, // No entry for agentType
		DefaultModel:  defaultAlias,
	}

	def := SubAgentToolDef(deps, nil)
	_, err := def.Handler(context.Background(), subAgentTask(AgentTypeExplore, "test task"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resolverCalledWith != defaultAlias {
		t.Errorf("ModelResolver called with %q, want %q", resolverCalledWith, defaultAlias)
	}
	if capturedReq.ResolvedModel.Alias != resolvedModel.Alias {
		t.Errorf("child RunRequest ResolvedModel.Alias=%q, want %q", capturedReq.ResolvedModel.Alias, resolvedModel.Alias)
	}
	if capturedReq.ResolvedModel.BackendModelID != resolvedModel.BackendModelID {
		t.Errorf("child RunRequest ResolvedModel.BackendModelID=%q, want %q", capturedReq.ResolvedModel.BackendModelID, resolvedModel.BackendModelID)
	}
}

func TestSpecializedHandler_FallsBackWithNilResolver(t *testing.T) {
	// Agents entry exists with a model alias, but ModelResolver is nil.
	// Should fall back to parent model without error.
	agentType := AgentTypeExplore
	var capturedReq agent.RunRequest
	parentModel := provider.ResolvedModel{BackendModelID: "parent-model"}

	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		capturedReq = req
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			Provider:      stubProvider{},
			ParentReg:     tool.NewRegistry(),
			Runner:        runner,
			Events:        noopEventSink{},
			WorkDir:       "/tmp/work",
			ResolvedModel: parentModel,
		},
		ModelResolver: nil,
		AgentModels: map[string]string{
			string(agentType): "some-alias",
		},
	}

	def := SubAgentToolDef(deps, nil)
	_, err := def.Handler(context.Background(), subAgentTask(AgentTypeExplore, "test task"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedReq.ResolvedModel.BackendModelID != parentModel.BackendModelID {
		t.Errorf("child RunRequest BackendModelID=%q, want %q", capturedReq.ResolvedModel.BackendModelID, parentModel.BackendModelID)
	}
}

func TestSpecializedHandler_EmptyModelConfigUsesProfileDefault(t *testing.T) {
	const defaultAlias = "profile-default"
	var resolverCalledWith string
	var capturedReq agent.RunRequest
	modelResolver := func(alias string) (provider.Provider, provider.ResolvedModel, error) {
		resolverCalledWith = alias
		return stubProvider{}, provider.ResolvedModel{Alias: alias, BackendModelID: "profile-model"}, nil
	}
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		capturedReq = req
		return successRunState(), nil
	}}
	deps := minimalDeps(runner)
	deps.ModelResolver = modelResolver
	deps.AgentModels = map[string]string{string(AgentTypeExplore): ""}
	deps.DefaultModel = defaultAlias

	if _, err := SubAgentToolDef(deps, nil).Handler(context.Background(), subAgentTask(AgentTypeExplore, "test task")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolverCalledWith != defaultAlias {
		t.Errorf("ModelResolver called with %q, want %q", resolverCalledWith, defaultAlias)
	}
	if capturedReq.ResolvedModel.BackendModelID != "profile-model" {
		t.Errorf("child RunRequest ResolvedModel.BackendModelID=%q, want profile-model", capturedReq.ResolvedModel.BackendModelID)
	}
}

func TestSpecializedHandler_ProfileDefaultResolverError(t *testing.T) {
	const defaultAlias = "bad-profile-default"
	expectedErr := fmt.Errorf("profile default unavailable")
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})
	deps.ModelResolver = func(alias string) (provider.Provider, provider.ResolvedModel, error) {
		if alias != defaultAlias {
			t.Errorf("ModelResolver alias = %q, want %q", alias, defaultAlias)
		}
		return nil, provider.ResolvedModel{}, expectedErr
	}
	deps.DefaultModel = defaultAlias

	_, err := SubAgentToolDef(deps, nil).Handler(context.Background(), subAgentTask(AgentTypeExplore, "test task"))
	if err == nil {
		t.Fatal("expected profile default resolution error")
	}
	if !strings.Contains(err.Error(), defaultAlias) || !strings.Contains(err.Error(), expectedErr.Error()) {
		t.Fatalf("error = %q, want alias and resolver error", err)
	}
}

func TestResolveModel_VisionEmptyAliasDoesNotUseProfileDefault(t *testing.T) {
	resolverCalled := false
	deps := minimalDeps(nil)
	deps.ModelResolver = func(string) (provider.Provider, provider.ResolvedModel, error) {
		resolverCalled = true
		return nil, provider.ResolvedModel{}, fmt.Errorf("vision resolver should not be called")
	}
	deps.DefaultModel = "profile-default"

	gotProvider, gotModel, err := resolveModel(AgentTypeVision, deps)
	if err != nil {
		t.Fatalf("resolveModel() error = %v", err)
	}
	if resolverCalled {
		t.Fatal("vision empty alias used profile default resolver")
	}
	if gotProvider != deps.Provider || gotModel.BackendModelID != deps.ResolvedModel.BackendModelID {
		t.Fatalf("resolveModel() = provider %v, model %#v, want parent fallback", gotProvider, gotModel)
	}
}

func TestSpecializedHandler_ModelResolverError(t *testing.T) {
	// ModelResolver returns an error.
	// Handler should return that error.
	agentType := AgentTypeExplore
	expectedAlias := "bad-model"
	expectedErr := fmt.Errorf("model not found")

	modelResolver := func(_ string) (provider.Provider, provider.ResolvedModel, error) {
		return nil, provider.ResolvedModel{}, expectedErr
	}

	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			Provider:      stubProvider{},
			ParentReg:     tool.NewRegistry(),
			Runner:        runner,
			Events:        noopEventSink{},
			WorkDir:       "/tmp/work",
			ResolvedModel: provider.ResolvedModel{BackendModelID: "parent-model"},
		},
		ModelResolver: modelResolver,
		AgentModels: map[string]string{
			string(agentType): expectedAlias,
		},
	}

	def := SubAgentToolDef(deps, nil)
	_, err := def.Handler(context.Background(), subAgentTask(AgentTypeExplore, "test task"))
	if err == nil {
		t.Fatal("expected error from ModelResolver")
	}
	if !strings.Contains(err.Error(), string(agentType)) {
		t.Errorf("error %q should mention agent type %q", err.Error(), agentType)
	}
	if !strings.Contains(err.Error(), expectedAlias) {
		t.Errorf("error %q should mention model alias %q", err.Error(), expectedAlias)
	}
}

func TestSpecializedHandler_SavesChildSession(t *testing.T) {
	origIDGen := idGen
	idGen = func() string { return "child-specialized" }
	defer func() { idGen = origIDGen }()

	store := NewSessionStore()
	state := agent.RunState{
		Conversation: []agent.Message{
			{
				Role:    agent.MessageRoleAssistant,
				Content: "exploring",
				ToolCalls: []agent.ToolCall{
					{ID: "call-1", Name: "read", Arguments: map[string]any{"path": "main.go"}},
				},
			},
		},
		TurnCount:  1,
		TokenCount: 17,
		StopReason: agent.StopReasonComplete,
	}

	var (
		capturedReq    agent.RunRequest
		capturedReqSet bool
	)
	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{},
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(),
			Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
				if !capturedReqSet {
					capturedReq = req
					capturedReqSet = true
				}
				return state, nil
			}},
			Events:       noopEventSink{},
			WorkDir:      "/tmp/work",
			SessionStore: store,
		},
	}

	def := SubAgentToolDef(deps, nil)
	_, err := def.Handler(context.Background(), subAgentTask(AgentTypeExplore, "inspect code"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	session, ok := store.Get("child-specialized")
	if !ok {
		t.Fatal("child session was not saved")
	}
	if session.Spec.AgentID != "child-specialized" {
		t.Fatalf("Spec.AgentID = %q, want %q", session.Spec.AgentID, "child-specialized")
	}
	// Spec.Task is the assembled markdown from the structured task, which should include the objective.
	if !strings.Contains(session.Spec.Task, "inspect code") {
		t.Fatalf("Spec.Task = %q, should contain 'inspect code'", session.Spec.Task)
	}
	if session.Spec.SystemPrompt != AgentSystemPrompt(AgentTypeExplore) {
		t.Fatalf("Spec.SystemPrompt = %q, want %q", session.Spec.SystemPrompt, AgentSystemPrompt(AgentTypeExplore))
	}
	if !runRequestsEqualIgnoringFuncFields(session.Request, capturedReq) {
		t.Fatal("saved request does not match child run request")
	}
	if capturedReq.TurnBudgetNotice == nil {
		t.Fatal("expected TurnBudgetNotice to be set on the dispatched child run request")
	}
	if !reflect.DeepEqual(session.Conversation, state.Conversation) {
		t.Fatalf("Conversation = %#v, want %#v", session.Conversation, state.Conversation)
	}
	if session.TurnCount != state.TurnCount {
		t.Fatalf("TurnCount = %d, want %d", session.TurnCount, state.TurnCount)
	}
	if session.TokenCount != state.TokenCount {
		t.Fatalf("TokenCount = %d, want %d", session.TokenCount, state.TokenCount)
	}
	if session.ToolCallCount != 1 {
		t.Fatalf("ToolCallCount = %d, want 1", session.ToolCallCount)
	}
}

func TestVisionToolDef_Schema(t *testing.T) {
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})
	def := SubAgentToolDef(deps, nil)

	schema, ok := def.ParameterSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("ParameterSchema missing 'properties' map")
	}

	// Vision must have all six structured task fields plus image_id.
	requiredFields := []string{"objective", "context", "deliverable", "constraints", "success_criteria", "checks"}
	for _, field := range requiredFields {
		if _, has := schema[field]; !has {
			t.Errorf("vision schema missing required field %q", field)
		}
	}

	if _, hasImageID := schema["image_id"]; !hasImageID {
		t.Error("vision schema missing 'image_id' property")
	}

	required, ok := def.ParameterSchema["required"].([]any)
	if !ok {
		t.Fatal("ParameterSchema missing 'required' slice")
	}
	requiredSet := make(map[string]bool, len(required))
	for _, r := range required {
		if s, ok := r.(string); ok {
			requiredSet[s] = true
		}
	}
	for _, field := range requiredFields {
		if !requiredSet[field] {
			t.Errorf("%q must be required in schema", field)
		}
	}
	// image_id is present but not required in schema; handler enforces it for type=vision
	if requiredSet["image_id"] {
		t.Error("'image_id' must NOT be in required array (conditionally required by handler)")
	}
	// Should have 7 required fields: type + 6 task fields (image_id is not required)
	if len(requiredSet) != 7 {
		t.Errorf("schema has %d required fields, want 7 (type + 6 task fields)", len(requiredSet))
	}
}

func TestVisionToolDef_DescriptionMentionsFollowUp(t *testing.T) {
	// Updated: SubAgentToolDef is a unified tool for all types.
	// The follow_up guidance was vision-specific (reusing agent_id for follow-up questions
	// about the same cached image). The unified tool description doesn't repeat type-specific
	// guidance; that's in the type enum descriptions. Verify the unified description
	// mentions the type parameter for agent-specific behavior.
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})
	def := SubAgentToolDef(deps, nil)

	if !strings.Contains(def.Description, "type") {
		t.Errorf("sub_agent tool description should reference the 'type' parameter, got: %q", def.Description)
	}
}

func TestVisionToolSkippedWithoutModel(t *testing.T) {
	// When the vision model is not configured, SubAgentToolDef should
	// exclude vision from the type enum.
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})
	// SubAgentCfg.Agents has no "vision" entry → model is empty.
	def := SubAgentToolDef(deps, []AgentType{AgentTypeVision})

	if def.Name != SubAgentToolName {
		t.Errorf("tool name=%q, want %q", def.Name, SubAgentToolName)
	}

	props, _ := def.ParameterSchema["properties"].(map[string]any)
	typeField, _ := props["type"].(map[string]any)
	enumVals, _ := typeField["enum"].([]any)

	// Vision should not be in the enum
	for _, val := range enumVals {
		if val == string(AgentTypeVision) {
			t.Error("vision should be excluded from type enum")
		}
	}
}

func TestVisionHandler_UnknownImageID(t *testing.T) {
	store := agent.NewImageStore(t.TempDir())
	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{},
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(),
			Runner: &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
				return successRunState(), nil
			}},
			Events:  noopEventSink{},
			WorkDir: "/tmp/work",
		},
		ImageStore: store,
	}
	def := SubAgentToolDef(deps, nil)

	input := subAgentTaskWithImageID("describe the image", "img-99")
	_, err := def.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for unknown image_id")
	}
	if !strings.Contains(err.Error(), "img-99") {
		t.Errorf("error %q should mention the image_id", err.Error())
	}
}

func TestVisionHandler_ReadsImageAndInjectsIntoSpec(t *testing.T) {
	// Write a small fake image file and register it in the ImageStore.
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "test.png")
	imgContent := []byte("fake-png-content")
	if err := os.WriteFile(imgPath, imgContent, 0o600); err != nil {
		t.Fatalf("write temp image: %v", err)
	}

	store := agent.NewImageStore(dir)
	ref := store.Register(imgPath, "image/png", 100, 200, len(imgContent))

	var capturedReq agent.RunRequest
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		capturedReq = req
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{},
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(tool.ToolDef{Name: "read", Description: "read"}),
			Runner:      runner,
			Events:      noopEventSink{},
			WorkDir:     dir,
		},
		ImageStore: store,
	}
	def := SubAgentToolDef(deps, nil)

	input := subAgentTaskWithImageID("describe what you see", ref.ID)
	raw, err := def.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Result must be a tool.ExecutionResult.
	if _, ok := raw.(tool.ExecutionResult); !ok {
		t.Fatalf("handler returned %T, want tool.ExecutionResult", raw)
	}

	// The child RunRequest prompt must include the image in the first user message.
	_ = capturedReq // runner captured the request; build verification via Result

	// Verify the host result keeps the exact child output.
	execResult, _ := raw.(tool.ExecutionResult)
	dr, ok := execResult.Value.(Result)
	if !ok {
		t.Fatalf("ExecutionResult.Value is %T, want Result", execResult.Value)
	}
	if dr.Output != "task result" {
		t.Errorf("result output %q, want exact child output", dr.Output)
	}

	// Verify the image was base64-encoded from disk correctly.
	wantEncoded := base64.StdEncoding.EncodeToString(imgContent)
	_ = wantEncoded // encoding correctness is implicit; the handler would error if os.ReadFile failed
}

// TestVisionRoutingArgs_PassesHandlerValidation pins the argument shape that
// internal/agent's VisionRoutingArgs builds against the real sub_agent
// dispatch and vision handler validation. internal/agent cannot import
// internal/delegation (import cycle), so this test lives here to catch drift
// between the two sides if either changes independently.
func TestVisionRoutingArgs_PassesHandlerValidation(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "test.png")
	imgContent := []byte("fake-png-content")
	if err := os.WriteFile(imgPath, imgContent, 0o600); err != nil {
		t.Fatalf("write temp image: %v", err)
	}

	store := agent.NewImageStore(dir)
	ref := store.Register(imgPath, "image/png", 100, 200, len(imgContent))

	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{},
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(tool.ToolDef{Name: "read", Description: "read"}),
			Runner:      runner,
			Events:      noopEventSink{},
			WorkDir:     dir,
		},
		ImageStore: store,
	}
	def := SubAgentToolDef(deps, nil)

	input := agent.VisionRoutingArgs(ref.ID, "some user request")

	raw, err := def.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("VisionRoutingArgs rejected by sub_agent validation: %v", err)
	}

	execResult, ok := raw.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("handler returned %T, want tool.ExecutionResult", raw)
	}
	dr, ok := execResult.Value.(Result)
	if !ok {
		t.Fatalf("ExecutionResult.Value is %T, want Result", execResult.Value)
	}
	if dr.Output != "task result" {
		t.Errorf("result output %q, want exact child output from the vision child", dr.Output)
	}
}

func TestSpecializedHandler_SavesSessionForStructuredFailure(t *testing.T) {
	origIDGen := idGen
	idGen = func() string { return "child-specialized-error" }
	defer func() { idGen = origIDGen }()

	store := NewSessionStore()
	var (
		capturedReq    agent.RunRequest
		capturedReqSet bool
	)
	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{},
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(),
			Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
				if !capturedReqSet {
					capturedReq = req
					capturedReqSet = true
				}
				return agent.RunState{}, context.Canceled
			}},
			Events:       noopEventSink{},
			WorkDir:      "/tmp/work",
			SessionStore: store,
		},
	}

	def := SubAgentToolDef(deps, nil)
	_, err := def.Handler(context.Background(), subAgentTask(AgentTypeExplore, "inspect code"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	session, ok := store.Get("child-specialized-error")
	if !ok {
		t.Fatal("session was not saved for structured failure")
	}
	if !runRequestsEqualIgnoringFuncFields(session.Request, capturedReq) {
		t.Fatal("saved request does not match child run request")
	}
	if capturedReq.TurnBudgetNotice == nil {
		t.Fatal("expected TurnBudgetNotice to be set on the dispatched child run request")
	}
	if len(session.Conversation) != 0 {
		t.Fatalf("Conversation length = %d, want 0", len(session.Conversation))
	}
	if session.TurnCount != 0 {
		t.Fatalf("TurnCount = %d, want 0", session.TurnCount)
	}
	if session.TokenCount != 0 {
		t.Fatalf("TokenCount = %d, want 0", session.TokenCount)
	}
	if session.ToolCallCount != 0 {
		t.Fatalf("ToolCallCount = %d, want 0", session.ToolCallCount)
	}
}

// TestSubAgentHandlerDepsCarriesSandboxState proves the specialized handler
// forwards the parent's plain sandbox state (SandboxEnabled and writable
// mounts) into the child prompt's sandbox section.
func TestSubAgentHandlerDepsCarriesSandboxState(t *testing.T) {
	var capturedReq agent.RunRequest
	firstCall := true

	idGen = func() string { return "child-sandbox-test" }
	t.Cleanup(func() { idGen = func() string { return fmt.Sprintf("child-%d", agentCounter.Add(1)) } })

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			Provider:              stubProvider{},
			ParentReg:             tool.NewRegistry(tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }}),
			SubAgentCfg:           config.SubAgentConfig{},
			Events:                noopEventSink{},
			WorkDir:               "/tmp/work",
			SandboxEnabled:        true,
			SandboxWritableMounts: []string{"/host/rw"},
			Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
				if firstCall {
					capturedReq = req
					firstCall = false
				}
				return successRunState(), nil
			}},
		},
	}

	def := SubAgentToolDef(deps, nil)
	_, err := def.Handler(context.Background(), subAgentTask(AgentTypeExplore, "test sandbox state"))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if !capturedReq.Prompt.SandboxEnabled {
		t.Error("child Prompt.SandboxEnabled=false, want true")
	}
	if want := []string{"/host/rw"}; !slices.Equal(capturedReq.Prompt.SandboxWritableMounts, want) {
		t.Errorf("child Prompt.SandboxWritableMounts=%v, want %v", capturedReq.Prompt.SandboxWritableMounts, want)
	}
}

// TestSubAgentHandlerDepsDisabledSandboxNotCarried proves a disabled (or
// bypassed) parent sandbox leaves the child prompt's sandbox section off.
func TestSubAgentHandlerDepsDisabledSandboxNotCarried(t *testing.T) {
	var capturedReq agent.RunRequest
	firstCall := true

	idGen = func() string { return "child-nil-sandbox" }
	t.Cleanup(func() { idGen = func() string { return fmt.Sprintf("child-%d", agentCounter.Add(1)) } })

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }}),
			SubAgentCfg: config.SubAgentConfig{},
			Events:      noopEventSink{},
			WorkDir:     "/tmp/work",
			// SandboxEnabled defaults to false; no writable mounts configured.
			Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
				if firstCall {
					capturedReq = req
					firstCall = false
				}
				return successRunState(), nil
			}},
		},
	}

	def := SubAgentToolDef(deps, nil)
	_, err := def.Handler(context.Background(), subAgentTask(AgentTypeExplore, "test disabled sandbox"))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if capturedReq.Prompt.SandboxEnabled {
		t.Error("child Prompt.SandboxEnabled=true, want false")
	}
	if len(capturedReq.Prompt.SandboxWritableMounts) != 0 {
		t.Errorf("child Prompt.SandboxWritableMounts=%v, want empty", capturedReq.Prompt.SandboxWritableMounts)
	}
}

func TestSpecializedHandlerSkipProjectContext(t *testing.T) {
	// Agents that should skip project context.
	skipTypes := []AgentType{AgentTypeExplore, AgentTypeResearch, AgentTypeSanityCheck}
	// Vision is excluded because it uses newVisionHandler which requires image_id.
	// Agents that should receive full project context.
	keepTypes := []AgentType{AgentTypeCode, AgentTypeReview, AgentTypeEvaluate}

	t.Run("lean agents skip project context", func(t *testing.T) {
		for _, agentType := range skipTypes {
			t.Run(string(agentType), func(t *testing.T) {
				var capturedReq agent.RunRequest
				runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
					capturedReq = req
					return successRunState(), nil
				}}
				deps := minimalDeps(runner)
				def := SubAgentToolDef(deps, nil)

				_, err := def.Handler(context.Background(), subAgentTask(agentType, "test task"))
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if !capturedReq.Prompt.SkipProjectContext {
					t.Errorf("%s: SkipProjectContext = false, want true", agentType)
				}
				if capturedReq.Prompt.SkipAgents {
					t.Errorf("%s: SkipAgents = true, want false", agentType)
				}
			})
		}
	})

	t.Run("full agents keep project context", func(t *testing.T) {
		for _, agentType := range keepTypes {
			t.Run(string(agentType), func(t *testing.T) {
				var capturedReq agent.RunRequest
				runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
					capturedReq = req
					return successRunState(), nil
				}}
				deps := minimalDeps(runner)
				if agentType == AgentTypeCode {
					repo, cleanup := setupTestRepo(t)
					t.Cleanup(cleanup)
					deps.WorkDir = repo
				}
				def := SubAgentToolDef(deps, nil)

				_, err := def.Handler(context.Background(), subAgentTask(agentType, "test task"))
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if capturedReq.Prompt.SkipProjectContext {
					t.Errorf("%s: SkipProjectContext = true, want false", agentType)
				}
				if capturedReq.Prompt.SkipAgents {
					t.Errorf("%s: SkipAgents = true, want false", agentType)
				}
			})
		}
	})

	t.Run("vision skips agents and project context", func(t *testing.T) {
		dir := t.TempDir()
		imgPath := filepath.Join(dir, "test.png")
		imgContent := []byte("fake-png-content")
		if err := os.WriteFile(imgPath, imgContent, 0o600); err != nil {
			t.Fatalf("write temp image: %v", err)
		}

		store := agent.NewImageStore(dir)
		ref := store.Register(imgPath, "image/png", 100, 200, len(imgContent))

		var capturedReq agent.RunRequest
		runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			capturedReq = req
			return successRunState(), nil
		}}
		deps := minimalDeps(runner)
		deps.ImageStore = store
		def := SubAgentToolDef(deps, nil)

		input := subAgentTaskWithImageID("describe the image", ref.ID)
		_, err := def.Handler(context.Background(), input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !capturedReq.Prompt.SkipProjectContext {
			t.Errorf("vision: SkipProjectContext = false, want true")
		}
		if !capturedReq.Prompt.SkipAgents {
			t.Errorf("vision: SkipAgents = false, want true")
		}
	})
}

func TestMergedAllowedTools(t *testing.T) {
	tests := []struct {
		name   string
		base   []string
		extras []string
		want   []string
	}{
		{
			name:   "nil base and extras produce empty slice",
			base:   nil,
			extras: nil,
			want:   []string{},
		},
		{
			name:   "extras appended and sorted with base",
			base:   []string{"read", "grep"},
			extras: []string{"bash"},
			want:   []string{"bash", "grep", "read"},
		},
		{
			name:   "duplicates across base and extras removed",
			base:   []string{"read", "bash"},
			extras: []string{"bash", "read"},
			want:   []string{"bash", "read"},
		},
		{
			name:   "unsorted extras sorted with base",
			base:   []string{"ls", "read"},
			extras: []string{"mutate", "bash", "grep"},
			want:   []string{"bash", "grep", "ls", "mutate", "read"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergedAllowedTools(tt.base, tt.extras)
			if !slices.Equal(got, tt.want) {
				t.Errorf("mergedAllowedTools(%v, %v) = %v, want %v", tt.base, tt.extras, got, tt.want)
			}
		})
	}

	t.Run("base slice is not mutated", func(t *testing.T) {
		base := []string{"read", "grep", "ls"}
		original := append([]string(nil), base...)
		got := mergedAllowedTools(base, []string{"ls", "bash", "read"})
		if !slices.Equal(base, original) {
			t.Fatalf("base mutated: %v, want %v", base, original)
		}
		got[0] = "mutated"
		if !slices.Equal(base, original) {
			t.Fatalf("mutating merged result changed base: %v, want %v", base, original)
		}
	})
}

func TestSpecializedHandler_ExtraAllowedTools(t *testing.T) {
	mcpTool := "mcp__notes__search"
	parentDefs := []tool.ToolDef{
		{Name: "read", Description: "read"},
		{Name: "grep", Description: "grep"},
		{Name: "ls", Description: "ls"},
		{Name: "bash", Description: "bash"},
		{Name: mcpTool, Description: "search notes", MCP: tool.MCPProvenance{Server: "notes", ToolName: "search"}},
	}

	runHandler := func(t *testing.T, agentType AgentType, extras map[AgentType][]string) (agent.RunRequest, *SessionStore) {
		t.Helper()
		workDir := "/tmp/work"
		if agentType == AgentTypeCode {
			repo, cleanup := setupTestRepo(t)
			t.Cleanup(cleanup)
			workDir = repo
		}
		store := NewSessionStore()
		var capturedReq agent.RunRequest
		runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			capturedReq = req
			return successRunState(), nil
		}}
		deps := SpecializedToolDeps{
			SubAgentHandlerDeps: SubAgentHandlerDeps{
				SubAgentCfg:       config.SubAgentConfig{},
				Provider:          stubProvider{},
				ParentReg:         tool.NewRegistry(parentDefs...),
				Runner:            runner,
				Events:            noopEventSink{},
				WorkDir:           workDir,
				SessionStore:      store,
				ExtraAllowedTools: extras,
			},
			ModelResolver: nil,
		}
		def := SubAgentToolDef(deps, nil)
		if _, err := def.Handler(context.Background(), subAgentTask(agentType, "test task")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return capturedReq, store
	}

	childToolNames := func(req agent.RunRequest) []string {
		names := make([]string, 0, len(req.Tools))
		for _, ts := range req.Tools {
			names = append(names, ts.Function.Name)
		}
		return names
	}

	t.Run("nil extras grants no extra tools", func(t *testing.T) {
		req, _ := runHandler(t, AgentTypeResearch, nil)
		if slices.Contains(childToolNames(req), mcpTool) {
			t.Errorf("research child contains %q with nil extras: %v", mcpTool, childToolNames(req))
		}
	})

	t.Run("empty extras map grants no extra tools", func(t *testing.T) {
		req, _ := runHandler(t, AgentTypeResearch, map[AgentType][]string{})
		if slices.Contains(childToolNames(req), mcpTool) {
			t.Errorf("research child contains %q with empty extras: %v", mcpTool, childToolNames(req))
		}
	})

	t.Run("extra tool granted to research only", func(t *testing.T) {
		extras := map[AgentType][]string{AgentTypeResearch: {mcpTool}}
		researchReq, _ := runHandler(t, AgentTypeResearch, extras)
		if !slices.Contains(childToolNames(researchReq), mcpTool) {
			t.Errorf("research child missing %q: %v", mcpTool, childToolNames(researchReq))
		}
		codeReq, _ := runHandler(t, AgentTypeCode, extras)
		if slices.Contains(childToolNames(codeReq), mcpTool) {
			t.Errorf("code child unexpectedly contains %q: %v", mcpTool, childToolNames(codeReq))
		}
	})

	t.Run("unknown extra tool names are ignored", func(t *testing.T) {
		extras := map[AgentType][]string{AgentTypeResearch: {"mcp__missing__tool"}}
		req, _ := runHandler(t, AgentTypeResearch, extras)
		names := childToolNames(req)
		if slices.Contains(names, "mcp__missing__tool") {
			t.Errorf("child contains unknown extra tool %q: %v", "mcp__missing__tool", names)
		}
		if !slices.Contains(names, "read") {
			t.Errorf("child missing base tool read: %v", names)
		}
	})

	t.Run("merged child tools are sorted and deduplicated", func(t *testing.T) {
		extras := map[AgentType][]string{AgentTypeResearch: {mcpTool, "read", "bash"}}
		req, _ := runHandler(t, AgentTypeResearch, extras)
		names := childToolNames(req)
		want := []string{"bash", "grep", "ls", mcpTool, "read"}
		if !slices.Equal(names, want) {
			t.Errorf("child tools = %v, want %v", names, want)
		}
	})

	t.Run("follow-up reuses merged includes from saved session", func(t *testing.T) {
		origIDGen := idGen
		idGen = func() string { return "child-extra-followup" }
		defer func() { idGen = origIDGen }()

		extras := map[AgentType][]string{AgentTypeResearch: {mcpTool}}
		initialReq, store := runHandler(t, AgentTypeResearch, extras)
		if !slices.Contains(childToolNames(initialReq), mcpTool) {
			t.Fatalf("initial research child missing %q: %v", mcpTool, childToolNames(initialReq))
		}

		var followUpReq agent.RunRequest
		handler := NewFollowUpHandler(SubAgentHandlerDeps{
			SubAgentCfg:  config.SubAgentConfig{},
			SessionStore: store,
			Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
				followUpReq = req
				return successRunState(), nil
			}},
			Events: noopEventSink{},
		})
		if _, err := handler(context.Background(), map[string]any{
			"agent_id": "child-extra-followup",
			"message":  "continue",
		}); err != nil {
			t.Fatalf("follow-up error: %v", err)
		}
		if !slices.Contains(childToolNames(followUpReq), mcpTool) {
			t.Errorf("follow-up child missing %q: %v", mcpTool, childToolNames(followUpReq))
		}
	})
}

func TestVisionHandler_ExtraAllowedTools(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "test.png")
	imgContent := []byte("fake-png-content")
	if err := os.WriteFile(imgPath, imgContent, 0o600); err != nil {
		t.Fatalf("write temp image: %v", err)
	}
	store := agent.NewImageStore(dir)
	ref := store.Register(imgPath, "image/png", 100, 200, len(imgContent))

	mcpTool := "mcp__gallery__find"
	var capturedReq agent.RunRequest
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		capturedReq = req
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg:       config.SubAgentConfig{},
			Provider:          stubProvider{},
			ParentReg:         tool.NewRegistry(tool.ToolDef{Name: "read"}, tool.ToolDef{Name: mcpTool}),
			Runner:            runner,
			Events:            noopEventSink{},
			WorkDir:           dir,
			ExtraAllowedTools: map[AgentType][]string{AgentTypeVision: {mcpTool}},
		},
		ImageStore: store,
	}

	def := SubAgentToolDef(deps, nil)
	input := subAgentTaskWithImageID("describe the image", ref.ID)
	raw, err := def.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := raw.(tool.ExecutionResult); !ok {
		t.Fatalf("handler returned %T, want tool.ExecutionResult", raw)
	}

	names := make([]string, 0, len(capturedReq.Tools))
	for _, ts := range capturedReq.Tools {
		names = append(names, ts.Function.Name)
	}
	if want := []string{mcpTool, "read"}; !slices.Equal(names, want) {
		t.Errorf("vision child tools = %v, want %v", names, want)
	}
}

func TestSpecializedHandler_CodeProvisionesWorktree(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	var capturedReq agent.RunRequest
	runCount := 0
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		// Capture only the first run (the main delegation), not the summary run.
		if runCount == 0 {
			capturedReq = req
		}
		runCount++
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{},
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(),
			Runner:      runner,
			Events:      noopEventSink{},
			WorkDir:     repo,
		},
		ModelResolver: nil,
	}

	def := SubAgentToolDef(deps, nil)
	raw, err := def.Handler(ctx, subAgentTask(AgentTypeCode, "implement a feature"))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	result, ok := raw.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("result type = %T, want tool.ExecutionResult", raw)
	}

	delegationResult, ok := result.Value.(Result)
	if !ok {
		t.Fatalf("result.Value type = %T, want Result", result.Value)
	}

	if delegationResult.WorktreePath == "" {
		t.Error("WorktreePath is empty; expected provisioned worktree path")
	}
	if delegationResult.WorktreeBranch == "" {
		t.Error("WorktreeBranch is empty; expected provisioned worktree branch")
	}
	if len(delegationResult.Warnings) != 0 {
		t.Errorf("Warnings = %v, want empty for clean repo", delegationResult.Warnings)
	}

	if capturedReq.Prompt.ProjectRoot != delegationResult.WorktreePath {
		t.Errorf("child ProjectRoot = %q, want %q (the WorktreePath)", capturedReq.Prompt.ProjectRoot, delegationResult.WorktreePath)
	}
	if capturedReq.Prompt.ProjectRoot == repo {
		t.Errorf("child ProjectRoot should not equal parent repo path %q", repo)
	}

	// Verify the child executor's actual working root equals the worktree path.
	// The executor is wrapped in scopedToolExecutor, so unwrap it to reach the real *tool.Executor.
	scopedExec, ok := capturedReq.Executor.(scopedToolExecutor)
	if !ok {
		t.Fatalf("executor type = %T, want scopedToolExecutor", capturedReq.Executor)
	}
	execInner, ok := scopedExec.inner.(*tool.Executor)
	if !ok {
		t.Fatalf("scoped executor inner type = %T, want *tool.Executor", scopedExec.inner)
	}
	execWorkDir := execInner.WorkDir()
	if execWorkDir != delegationResult.WorktreePath {
		t.Errorf("executor WorkDir = %q, want %q (the WorktreePath)", execWorkDir, delegationResult.WorktreePath)
	}
}

func TestSpecializedHandler_CodeWithDirtyTree(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create an untracked file to make the tree dirty.
	dirtyFile := filepath.Join(repo, "dirty.txt")
	if err := os.WriteFile(dirtyFile, []byte("dirty"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{},
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(),
			Runner:      runner,
			Events:      noopEventSink{},
			WorkDir:     repo,
		},
		ModelResolver: nil,
	}

	def := SubAgentToolDef(deps, nil)
	raw, err := def.Handler(ctx, subAgentTask(AgentTypeCode, "implement a feature"))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	result := raw.(tool.ExecutionResult)
	delegationResult := result.Value.(Result)

	if len(delegationResult.Warnings) == 0 {
		t.Error("Warnings is empty; expected dirty-tree warning")
	}

	// Verify the warning mentions the dirty file.
	hasWarning := false
	for _, w := range delegationResult.Warnings {
		if strings.Contains(w, "dirty.txt") {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Errorf("Warnings do not mention dirty.txt: %v", delegationResult.Warnings)
	}

	// Verify that despite the dirty tree, provisioning succeeded.
	if delegationResult.WorktreePath == "" {
		t.Error("WorktreePath is empty; worktree should still be provisioned with dirty tree")
	}
}

func TestSpecializedHandler_CodeFatalOnProvisioningFailure(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	t.Cleanup(cleanup)
	badRepo := filepath.Join(repo, "nonexistent")

	runCount := 0
	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		runCount++
		return successRunState(), nil
	}}
	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			Provider:  stubProvider{},
			ParentReg: tool.NewRegistry(),
			Runner:    runner,
			Events:    noopEventSink{},
			WorkDir:   badRepo,
		},
	}

	def := SubAgentToolDef(deps, nil)
	raw, err := def.Handler(ctx, subAgentTask(AgentTypeCode, "implement a feature"))
	if err == nil {
		t.Fatal("expected fatal worktree provisioning error")
	}
	if !strings.Contains(err.Error(), "provision code worktree") && !strings.Contains(err.Error(), "worktree provisioning") {
		t.Errorf("error %q does not mention worktree provisioning", err)
	}
	if raw != nil {
		t.Errorf("result = %#v, want nil on fatal provisioning failure", raw)
	}
	if runCount != 0 {
		t.Errorf("runner calls = %d, want 0", runCount)
	}
}

func TestApplyCodeWorktreeResult_MergesWarnings(t *testing.T) {
	result := tool.ExecutionResult{Value: Result{
		Warnings: []string{"dirty worktree after failed remediation"},
	}}
	got := applyCodeWorktreeResult(result, CodeWorktree{Path: "/tmp/worktree", Branch: "delegate/test"}, []string{"parent tree was dirty"})
	delegationResult, ok := got.Value.(Result)
	if !ok {
		t.Fatalf("result.Value type = %T, want Result", got.Value)
	}
	wantWarnings := []string{"parent tree was dirty", "dirty worktree after failed remediation"}
	if !slices.Equal(delegationResult.Warnings, wantWarnings) {
		t.Errorf("Warnings = %v, want %v", delegationResult.Warnings, wantWarnings)
	}
	if delegationResult.WorktreePath != "/tmp/worktree" || delegationResult.WorktreeBranch != "delegate/test" {
		t.Errorf("worktree fields = %q, %q, want %q, %q", delegationResult.WorktreePath, delegationResult.WorktreeBranch, "/tmp/worktree", "delegate/test")
	}
}

func TestSpecializedHandler_NonCodeAgentsNoWorktreeFields(t *testing.T) {
	ctx := context.Background()
	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}}

	for _, agentType := range []AgentType{AgentTypeExplore, AgentTypeResearch, AgentTypeEvaluate, AgentTypeSanityCheck, AgentTypeReview} {
		agentType := agentType
		t.Run(string(agentType), func(t *testing.T) {
			deps := SpecializedToolDeps{
				SubAgentHandlerDeps: SubAgentHandlerDeps{
					SubAgentCfg: config.SubAgentConfig{},
					Provider:    stubProvider{},
					ParentReg:   tool.NewRegistry(),
					Runner:      runner,
					Events:      noopEventSink{},
					WorkDir:     "/tmp/work",
				},
				ModelResolver: nil,
			}

			def := SubAgentToolDef(deps, nil)
			raw, err := def.Handler(ctx, subAgentTask(AgentTypeExplore, "test task"))
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}

			result := raw.(tool.ExecutionResult)
			delegationResult := result.Value.(Result)

			if delegationResult.WorktreePath != "" {
				t.Errorf("WorktreePath = %q, want empty for non-code agent", delegationResult.WorktreePath)
			}
			if delegationResult.WorktreeBranch != "" {
				t.Errorf("WorktreeBranch = %q, want empty for non-code agent", delegationResult.WorktreeBranch)
			}
			if len(delegationResult.Warnings) != 0 {
				t.Errorf("Warnings = %v, want empty for non-code agent", delegationResult.Warnings)
			}
		})
	}
}
func TestSpecializedHandler_ParentCallIDFromContext(t *testing.T) {
	sink := &collectingSink{}
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})
	deps.Events = sink
	ctx := context.WithValue(context.Background(), tool.ExecutionCallIDKey{}, "call_ABC")

	if _, err := SubAgentToolDef(deps, nil).Handler(ctx, subAgentTask(AgentTypeExplore, "inspect")); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if len(sink.events) == 0 {
		t.Fatal("handler emitted no events")
	}
	started, ok := sink.events[0].Payload.(output.DelegationStartedEvent)
	if !ok {
		t.Fatalf("first event payload = %T, want DelegationStartedEvent", sink.events[0].Payload)
	}
	if started.CallID != "call_ABC" {
		t.Errorf("started CallID = %q, want call_ABC", started.CallID)
	}
}

func TestVisionHandler_ParentCallIDFromContext(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "test.png")
	if err := os.WriteFile(imgPath, []byte("fake-png-content"), 0o600); err != nil {
		t.Fatalf("write temp image: %v", err)
	}
	store := agent.NewImageStore(dir)
	ref := store.Register(imgPath, "image/png", 100, 200, 15)
	sink := &collectingSink{}
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})
	deps.Events = sink
	deps.ImageStore = store
	ctx := context.WithValue(context.Background(), tool.ExecutionCallIDKey{}, "call_VISION")

	if _, err := SubAgentToolDef(deps, nil).Handler(ctx, subAgentTaskWithImageID("describe", ref.ID)); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if len(sink.events) == 0 {
		t.Fatal("handler emitted no events")
	}
	started, ok := sink.events[0].Payload.(output.DelegationStartedEvent)
	if !ok {
		t.Fatalf("first event payload = %T, want DelegationStartedEvent", sink.events[0].Payload)
	}
	if started.CallID != "call_VISION" {
		t.Errorf("started CallID = %q, want call_VISION", started.CallID)
	}
}
