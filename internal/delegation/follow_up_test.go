package delegation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func TestFollowUpToolDef(t *testing.T) {
	called := false
	def := FollowUpToolDef(func(_ context.Context, _ map[string]any) (any, error) {
		called = true
		return nil, nil
	})

	if def.Name != FollowUpToolName {
		t.Fatalf("Name=%q, want %q", def.Name, FollowUpToolName)
	}
	const wantDescription = "Continue work with an existing sub-agent by sending a follow-up message. Use this to resume a suitable warm agent for the same bounded deliverable in the same live workspace, sequentially, to guide incomplete work, request refinements, make related corrections with the responsible implementation agent, or request a narrow re-check from the original reviewer. Use fresh delegation for unavailable or non-resumable sessions, material lane or scope changes, independent or wider review, or removed worktrees; workflow handoffs are not safe continuation boundaries."
	if def.Description != wantDescription {
		t.Fatalf("Description=%q, want %q", def.Description, wantDescription)
	}
	props, ok := def.ParameterSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties missing from schema")
	}
	for _, key := range []string{"agent_id", "message"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("property %q missing from schema", key)
		}
	}
	required, ok := def.ParameterSchema["required"].([]any)
	if !ok || !reflect.DeepEqual(required, []any{"agent_id", "message"}) {
		t.Fatalf("required=%v, want [agent_id message]", required)
	}
	if _, err := def.Handler(context.Background(), nil); err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if !called {
		t.Fatal("handler was not called")
	}
}

func TestFollowUpHandler_UnknownAgentID(t *testing.T) {
	handler := NewFollowUpHandler(SubAgentHandlerDeps{SessionStore: NewSessionStore()})

	_, err := handler(context.Background(), map[string]any{
		"agent_id": "missing",
		"message":  "continue",
	})
	if err == nil {
		t.Fatal("expected error for unknown agent id")
	}
	if got, want := err.Error(), `follow_up: no session for agent "missing"`; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestFollowUpHandler_RetainsConversationAndResetsBudget(t *testing.T) {
	store := NewSessionStore()
	store.Save(&ChildSession{
		Spec: Spec{
			AgentID:   "child-1",
			AgentType: AgentTypeReview,
			Task:      "inspect code",
			Limits: Limits{
				MaxTurns:          2,
				OutputLimitTokens: 9,
			},
		},
		Request: agent.RunRequest{
			Prompt: promptWithConversation("initial task"),
			Limits: agent.Limits{MaxTurns: 2, MaxTokens: 9},
		},
		Conversation: []agent.Message{
			{Role: agent.MessageRoleUser, Content: "initial task"},
			{Role: agent.MessageRoleAssistant, Content: "first answer"},
		},
		TurnCount:     2,
		TokenCount:    40,
		ToolCallCount: 1,
	})

	var capturedReq agent.RunRequest
	events := &recordingEventSink{}
	handler := NewFollowUpHandler(SubAgentHandlerDeps{
		SubAgentCfg:  config.SubAgentConfig{MaxTurns: 7, MaxTokens: 77},
		Events:       events,
		SessionStore: store,
		Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			capturedReq = req
			return agent.RunState{
				Conversation: []agent.Message{
					{Role: agent.MessageRoleUser, Content: "initial task"},
					{Role: agent.MessageRoleAssistant, Content: "first answer"},
					{Role: agent.MessageRoleUser, Content: "continue with more detail"},
					{
						Role:    agent.MessageRoleAssistant,
						Content: "second answer",
						ToolCalls: []agent.ToolCall{
							{ID: "call-2", Name: "read", Arguments: map[string]any{"path": "main.go"}},
						},
					},
				},
				TurnCount:  3,
				TokenCount: 15,
				StopReason: agent.StopReasonComplete,
			}, nil
		}},
	})

	got, err := handler(context.Background(), map[string]any{
		"agent_id": "child-1",
		"message":  "continue with more detail",
	})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if capturedReq.Limits.MaxTurns != 9 {
		t.Fatalf("MaxTurns=%d, want 9 (session.TurnCount 2 + fresh MaxTurns 7)", capturedReq.Limits.MaxTurns)
	}
	if capturedReq.Limits.MaxTokens != 77 {
		t.Fatalf("MaxTokens=%d, want 77", capturedReq.Limits.MaxTokens)
	}
	if len(capturedReq.Prompt.Conversation) != 3 {
		t.Fatalf("conversation length = %d, want 3", len(capturedReq.Prompt.Conversation))
	}
	if capturedReq.Prompt.Conversation[0].Content != "initial task" || capturedReq.Prompt.Conversation[1].Content != "first answer" {
		t.Fatalf("prior conversation was not retained: %#v", capturedReq.Prompt.Conversation)
	}
	last := capturedReq.Prompt.Conversation[len(capturedReq.Prompt.Conversation)-1]
	if last.Role != provider.MessageRoleUser || last.Content != "continue with more detail" {
		t.Fatalf("last follow-up message = %#v, want appended user follow-up", last)
	}

	result, ok := got.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("result type = %T, want tool.ExecutionResult", got)
	}
	delegationResult, ok := result.Value.(Result)
	if !ok {
		t.Fatalf("result.Value type = %T, want Result", result.Value)
	}
	if delegationResult.FollowUpCount != 1 {
		t.Fatalf("FollowUpCount=%d, want 1", delegationResult.FollowUpCount)
	}
	started := startedEvents(events.Events())
	if len(started) != 1 {
		t.Fatalf("started events = %d, want 1", len(started))
	}
	payload, ok := started[0].Payload.(output.DelegationStartedEvent)
	if !ok {
		t.Fatalf("started payload = %T, want DelegationStartedEvent", started[0].Payload)
	}
	if payload.AgentType != string(AgentTypeReview) || started[0].Scope.AgentID != "child-1" || started[0].Scope.AgentType != string(AgentTypeReview) {
		t.Errorf("started type/scope = %q/%+v, want %q and agent scope", payload.AgentType, started[0].Scope, AgentTypeReview)
	}

	session, ok := store.Get("child-1")
	if !ok {
		t.Fatal("session missing after follow-up")
	}
	if session.FollowUpCount != 1 {
		t.Fatalf("stored FollowUpCount=%d, want 1", session.FollowUpCount)
	}
	if session.TurnCount != 5 {
		t.Fatalf("stored TurnCount=%d, want 5", session.TurnCount)
	}
	if session.TokenCount != 55 {
		t.Fatalf("stored TokenCount=%d, want 55", session.TokenCount)
	}
	if session.ToolCallCount != 2 {
		t.Fatalf("stored ToolCallCount=%d, want 2", session.ToolCallCount)
	}
}

func TestFollowUpHandler_MultipleFollowUpsAccumulateStats(t *testing.T) {
	store := NewSessionStore()
	store.Save(&ChildSession{
		Spec: Spec{AgentID: "child-2", Task: "inspect code"},
		Request: agent.RunRequest{
			Prompt: promptWithConversation("initial task"),
			Limits: agent.Limits{MaxTurns: 1, MaxTokens: 1},
		},
		Conversation: []agent.Message{
			{Role: agent.MessageRoleUser, Content: "initial task"},
			{Role: agent.MessageRoleAssistant, Content: "first answer"},
		},
		TurnCount:     1,
		TokenCount:    10,
		ToolCallCount: 0,
	})

	call := 0
	handler := NewFollowUpHandler(SubAgentHandlerDeps{
		SubAgentCfg:  config.SubAgentConfig{MaxTurns: 5, MaxTokens: 50},
		SessionStore: store,
		Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			call++
			return agent.RunState{
				Conversation: providerToAgentMessages(req.Prompt.Conversation),
				TurnCount:    call,
				TokenCount:   5 * call,
				StopReason:   agent.StopReasonComplete,
			}, nil
		}},
	})

	first, err := handler(context.Background(), map[string]any{"agent_id": "child-2", "message": "follow up one"})
	if err != nil {
		t.Fatalf("first follow-up error: %v", err)
	}
	second, err := handler(context.Background(), map[string]any{"agent_id": "child-2", "message": "follow up two"})
	if err != nil {
		t.Fatalf("second follow-up error: %v", err)
	}

	firstResult := first.(tool.ExecutionResult).Value.(Result)
	secondResult := second.(tool.ExecutionResult).Value.(Result)
	if firstResult.FollowUpCount != 1 {
		t.Fatalf("first FollowUpCount=%d, want 1", firstResult.FollowUpCount)
	}
	if secondResult.FollowUpCount != 2 {
		t.Fatalf("second FollowUpCount=%d, want 2", secondResult.FollowUpCount)
	}

	session, ok := store.Get("child-2")
	if !ok {
		t.Fatal("session missing after follow-ups")
	}
	if session.FollowUpCount != 2 {
		t.Fatalf("stored FollowUpCount=%d, want 2", session.FollowUpCount)
	}
	if session.TurnCount != 4 {
		t.Fatalf("stored TurnCount=%d, want 4", session.TurnCount)
	}
	if session.TokenCount != 25 {
		t.Fatalf("stored TokenCount=%d, want 25", session.TokenCount)
	}
}

func TestFollowUpHandler_ResumesFailedChildWhenSessionExists(t *testing.T) {
	store := NewSessionStore()
	store.Save(&ChildSession{
		Spec:          Spec{AgentID: "child-failed", Task: "inspect code"},
		Request:       agent.RunRequest{Prompt: promptWithConversation("initial task")},
		Conversation:  nil,
		TurnCount:     0,
		TokenCount:    0,
		ToolCallCount: 0,
	})

	handler := NewFollowUpHandler(SubAgentHandlerDeps{
		SubAgentCfg:  config.SubAgentConfig{MaxTurns: 5, MaxTokens: 50},
		SessionStore: store,
		Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			if len(req.Prompt.Conversation) != 1 {
				return agent.RunState{}, errors.New("expected follow-up to start from appended user message")
			}
			last := req.Prompt.Conversation[0]
			if last.Content != "retry with narrower scope" {
				return agent.RunState{}, errors.New("expected follow-up message to be appended")
			}
			return agent.RunState{
				Conversation: []agent.Message{
					{Role: agent.MessageRoleUser, Content: "retry with narrower scope"},
					{Role: agent.MessageRoleAssistant, Content: "recovered"},
				},
				TurnCount:  1,
				TokenCount: 8,
				StopReason: agent.StopReasonComplete,
			}, nil
		}},
	})

	got, err := handler(context.Background(), map[string]any{
		"agent_id": "child-failed",
		"message":  "retry with narrower scope",
	})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	result := got.(tool.ExecutionResult).Value.(Result)
	if result.Status != StatusComplete {
		t.Fatalf("Status=%q, want %q", result.Status, StatusComplete)
	}
	if result.FollowUpCount != 1 {
		t.Fatalf("FollowUpCount=%d, want 1", result.FollowUpCount)
	}

	session, ok := store.Get("child-failed")
	if !ok {
		t.Fatal("session missing after failed-child follow-up")
	}
	if session.FollowUpCount != 1 {
		t.Fatalf("stored FollowUpCount=%d, want 1", session.FollowUpCount)
	}
	if len(session.Conversation) != 2 {
		t.Fatalf("stored conversation length=%d, want 2", len(session.Conversation))
	}
}
func TestFollowUpHandler_CodeRemediationOnlyForProvisionedCodeSession(t *testing.T) {
	tests := []struct {
		name               string
		tools              []provider.ToolSpec
		remediation        *RemediationConfig
		wantRemediation    bool
		wantExpectedPath   string
		wantExpectedBranch string
	}{
		{
			name:  "code session",
			tools: []provider.ToolSpec{{Function: provider.ToolFunctionSpec{Name: "mutate"}}},
			remediation: &RemediationConfig{
				WorktreePath:   "/tmp/code-worktree",
				ExpectedBranch: "delegate/code-session",
				IsDirty: func(context.Context) ([]string, error) {
					return []string{"changed.go"}, nil
				},
				Head:      func(context.Context) (string, error) { return "before-remediation", nil },
				Committed: func(context.Context, string, []string) (bool, error) { return true, nil },
			},
			wantRemediation:    true,
			wantExpectedPath:   "/tmp/code-worktree",
			wantExpectedBranch: "delegate/code-session",
		},
		{
			name:  "non-code session",
			tools: []provider.ToolSpec{{Function: provider.ToolFunctionSpec{Name: "read"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewSessionStore()
			store.Save(&ChildSession{
				Spec:         Spec{AgentID: "follow-up-agent", Task: "continue work"},
				Request:      agent.RunRequest{Prompt: promptWithConversation("initial task"), Tools: tt.tools},
				Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "initial result"}},
				TurnCount:    1,
				Remediation:  tt.remediation,
			})

			remediationCalls := 0
			remediationDirtyChecks := 0
			var remediationRequest agent.RunRequest
			if tt.remediation != nil {
				isDirty := tt.remediation.IsDirty
				tt.remediation.IsDirty = func(ctx context.Context) ([]string, error) {
					remediationDirtyChecks++
					if remediationDirtyChecks == 1 {
						return isDirty(ctx)
					}
					return nil, nil
				}
			}
			handler := NewFollowUpHandler(SubAgentHandlerDeps{
				SubAgentCfg:  config.SubAgentConfig{MaxTurns: 5, MaxTokens: 50},
				SessionStore: store,
				Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
					if len(req.Prompt.Conversation) > 0 && strings.Contains(req.Prompt.Conversation[len(req.Prompt.Conversation)-1].Content, "Pre-remediation HEAD") {
						remediationCalls++
						remediationRequest = req
					}
					return agent.RunState{
						Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "follow-up result"}},
						TurnCount:    1,
						TokenCount:   5,
						StopReason:   agent.StopReasonComplete,
					}, nil
				}},
			})

			got, err := handler(context.Background(), map[string]any{
				"agent_id": "follow-up-agent",
				"message":  "continue",
			})
			if err != nil {
				t.Fatalf("handler returned error: %v", err)
			}
			result := got.(tool.ExecutionResult).Value.(Result)
			if (remediationCalls > 0) != tt.wantRemediation {
				t.Fatalf("remediation calls = %d, want remediation=%t", remediationCalls, tt.wantRemediation)
			}
			if tt.wantRemediation {
				if !strings.Contains(result.Output, "<remediation note: committed remaining changes; worktree left clean>") {
					t.Fatalf("output = %q, missing remediation note", result.Output)
				}
				prompt := remediationRequest.Prompt.Conversation[len(remediationRequest.Prompt.Conversation)-1].Content
				if !strings.Contains(prompt, tt.wantExpectedPath) || !strings.Contains(prompt, tt.wantExpectedBranch) {
					t.Fatalf("remediation prompt = %q, want path %q and branch %q", prompt, tt.wantExpectedPath, tt.wantExpectedBranch)
				}
			} else if strings.Contains(result.Output, "<remediation note:") {
				t.Fatalf("non-code follow-up output = %q, unexpectedly contains remediation note", result.Output)
			}
			if tt.wantRemediation {
				session, ok := store.Get("follow-up-agent")
				if !ok {
					t.Fatal("session missing after code follow-up")
				}
				if session.Remediation != tt.remediation {
					t.Fatal("remediation state was not preserved in session")
				}
				if session.FollowUpCount != 1 || session.TurnCount != 2 || session.TokenCount != 10 {
					t.Fatalf("session stats = (follow-ups=%d, turns=%d, tokens=%d), want (1, 2, 10)", session.FollowUpCount, session.TurnCount, session.TokenCount)
				}
			}
		})
	}
}

func TestFollowUpHandler_DeniesMutateChildInPlanMode(t *testing.T) {
	store := NewSessionStore()
	store.Save(&ChildSession{
		Spec: Spec{AgentID: "child-mutate", Task: "fix bug"},
		Request: agent.RunRequest{
			Prompt: promptWithConversation("initial task"),
			Tools:  []provider.ToolSpec{{Function: provider.ToolFunctionSpec{Name: "mutate"}}},
		},
		Conversation: []agent.Message{
			{Role: agent.MessageRoleUser, Content: "initial task"},
		},
	})

	runs := 0
	handler := NewFollowUpHandler(SubAgentHandlerDeps{
		SessionStore: store,
		Runner: &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
			runs++
			return agent.RunState{}, nil
		}},
	})

	ctx := context.WithValue(context.Background(), tool.ExecutionModeKey{}, config.ExecutionModePlan)
	_, err := handler(ctx, map[string]any{
		"agent_id": "child-mutate",
		"message":  "continue",
	})
	if err == nil {
		t.Fatal("expected error denying follow-up in plan mode")
	}
	want := "follow_up: plan mode is active; the code sub-agent (which can mutate files) is unavailable. " +
		"Ask the user to switch to build mode, or call workflow_handoff when your plan is ready"
	if got := err.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	if runs != 0 {
		t.Fatalf("runs = %d, want 0 (child runner must not be invoked when denied)", runs)
	}
}

func TestFollowUpHandler_AllowsMutateChildInBuildMode(t *testing.T) {
	store := NewSessionStore()
	store.Save(&ChildSession{
		Spec: Spec{AgentID: "child-mutate", Task: "fix bug"},
		Request: agent.RunRequest{
			Prompt: promptWithConversation("initial task"),
			Tools:  []provider.ToolSpec{{Function: provider.ToolFunctionSpec{Name: "mutate"}}},
		},
		Conversation: []agent.Message{
			{Role: agent.MessageRoleUser, Content: "initial task"},
		},
	})

	runs := 0
	handler := NewFollowUpHandler(SubAgentHandlerDeps{
		SessionStore: store,
		Runner: &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
			runs++
			return agent.RunState{
				Conversation: []agent.Message{
					{Role: agent.MessageRoleUser, Content: "initial task"},
					{Role: agent.MessageRoleAssistant, Content: "done"},
				},
				TurnCount:  1,
				TokenCount: 5,
				StopReason: agent.StopReasonComplete,
			}, nil
		}},
	})

	ctx := context.WithValue(context.Background(), tool.ExecutionModeKey{}, config.ExecutionModeBuild)
	_, err := handler(ctx, map[string]any{
		"agent_id": "child-mutate",
		"message":  "continue",
	})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if runs != 1 {
		t.Fatalf("runs = %d, want 1 (child runner must be invoked in build mode)", runs)
	}
}

func TestFollowUpHandler_NonMutateChildNotDeniedInPlanMode(t *testing.T) {
	store := NewSessionStore()
	store.Save(&ChildSession{
		Spec: Spec{AgentID: "child-readonly", Task: "investigate"},
		Request: agent.RunRequest{
			Prompt: promptWithConversation("initial task"),
			Tools: []provider.ToolSpec{
				{Function: provider.ToolFunctionSpec{Name: "read"}},
				{Function: provider.ToolFunctionSpec{Name: "grep"}},
			},
		},
		Conversation: []agent.Message{
			{Role: agent.MessageRoleUser, Content: "initial task"},
		},
	})

	runs := 0
	handler := NewFollowUpHandler(SubAgentHandlerDeps{
		SessionStore: store,
		Runner: &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
			runs++
			return agent.RunState{
				Conversation: []agent.Message{
					{Role: agent.MessageRoleUser, Content: "initial task"},
					{Role: agent.MessageRoleAssistant, Content: "found it"},
				},
				TurnCount:  1,
				TokenCount: 5,
				StopReason: agent.StopReasonComplete,
			}, nil
		}},
	})

	ctx := context.WithValue(context.Background(), tool.ExecutionModeKey{}, config.ExecutionModePlan)
	_, err := handler(ctx, map[string]any{
		"agent_id": "child-readonly",
		"message":  "continue",
	})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if runs != 1 {
		t.Fatalf("runs = %d, want 1 (non-mutate child must not be denied in plan mode)", runs)
	}
}

func TestChildHasMutateTool(t *testing.T) {
	tests := []struct {
		name  string
		tools []provider.ToolSpec
		want  bool
	}{
		{
			name:  "contains mutate",
			tools: []provider.ToolSpec{{Function: provider.ToolFunctionSpec{Name: "read"}}, {Function: provider.ToolFunctionSpec{Name: "mutate"}}},
			want:  true,
		},
		{
			name:  "without mutate",
			tools: []provider.ToolSpec{{Function: provider.ToolFunctionSpec{Name: "read"}}, {Function: provider.ToolFunctionSpec{Name: "grep"}}},
			want:  false,
		},
		{
			name:  "empty list",
			tools: []provider.ToolSpec{},
			want:  false,
		},
		{
			name:  "nil list",
			tools: nil,
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := childHasMutateTool(agent.RunRequest{Tools: tt.tools})
			if got != tt.want {
				t.Fatalf("childHasMutateTool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func promptWithConversation(contents ...string) prompt.AssemblyOptions {
	conversation := make([]provider.Message, 0, len(contents))
	for _, content := range contents {
		conversation = append(conversation, provider.Message{Role: provider.MessageRoleUser, Content: content})
	}
	return prompt.AssemblyOptions{Conversation: conversation}
}

func providerToAgentMessages(messages []provider.Message) []agent.Message {
	out := make([]agent.Message, 0, len(messages))
	for _, message := range messages {
		out = append(out, agent.Message{
			Role:    agent.MessageRole(message.Role),
			Content: message.Content,
			Turn:    message.Turn,
		})
	}
	return out
}
func TestFollowUpHandler_FreshBudgetWithHighPriorTurnCount(t *testing.T) {
	store := NewSessionStore()

	// Build a conversation with 58 prior turns (simulating a long prior session).
	conv := make([]agent.Message, 58)
	for i := 0; i < 58; i++ {
		role := agent.MessageRoleUser
		if i%2 == 1 {
			role = agent.MessageRoleAssistant
		}
		conv[i] = agent.Message{
			Role:    role,
			Content: fmt.Sprintf("message-%d", i+1),
			Turn:    i + 1,
		}
	}

	store.Save(&ChildSession{
		Spec: Spec{
			AgentID: "child-budget",
			Task:    "big task",
			Limits:  Limits{MaxTurns: 58, OutputLimitTokens: 100000},
		},
		Request: agent.RunRequest{
			Prompt: promptWithConversation("initial task"),
			Limits: agent.Limits{MaxTurns: 58, MaxTokens: 100000},
		},
		Conversation:  conv,
		TurnCount:     58,
		TokenCount:    5000,
		ToolCallCount: 10,
	})

	var capturedReq agent.RunRequest
	runs := 0
	handler := NewFollowUpHandler(SubAgentHandlerDeps{
		SubAgentCfg:  config.SubAgentConfig{MaxTurns: 30, MaxTokens: 100000},
		SessionStore: store,
		Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			capturedReq = req
			runs++

			// Simulate the real runner's stopRunBeforeTurn check:
			// state.TurnCount = max Turn value from conversation.
			maxTurn := 0
			for _, msg := range req.Prompt.Conversation {
				if msg.Turn > maxTurn {
					maxTurn = msg.Turn
				}
			}
			if req.Limits.MaxTurns > 0 && maxTurn >= req.Limits.MaxTurns {
				return agent.RunState{
					StopReason: agent.StopReasonMaxTurns,
					TurnCount:  maxTurn,
				}, nil
			}

			// Return a successful completion after consuming 1 new turn.
			return agent.RunState{
				Conversation: providerToAgentMessages(req.Prompt.Conversation),
				TurnCount:    maxTurn + 1,
				TokenCount:   5001,
				StopReason:   agent.StopReasonComplete,
			}, nil
		}},
	})

	got, err := handler(context.Background(), map[string]any{
		"agent_id": "child-budget",
		"message":  "continue the work",
	})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	// Verify the handler set MaxTurns to include prior turn count,
	// so the runner does not immediately stop at StopReasonMaxTurns.
	if capturedReq.Limits.MaxTurns != 58+30 {
		t.Fatalf("MaxTurns=%d, want %d (session.TurnCount 58 + fresh MaxTurns 30)", capturedReq.Limits.MaxTurns, 58+30)
	}

	// Verify the runner was actually invoked (not immediately stopped by turn limit).
	if runs == 0 {
		t.Fatal("follow-up did not run any turns — cumulative TurnCount blocked fresh budget")
	}

	// Verify the follow-up produced a successful result.
	result, ok := got.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("result type = %T, want tool.ExecutionResult", got)
	}
	delegationResult, ok := result.Value.(Result)
	if !ok {
		t.Fatalf("result.Value type = %T, want Result", result.Value)
	}
	if delegationResult.FollowUpCount != 1 {
		t.Fatalf("FollowUpCount=%d, want 1", delegationResult.FollowUpCount)
	}
}

func TestFollowUpHandler_AccumulatesTokenUsageFromPriorSession(t *testing.T) {
	store := NewSessionStore()
	store.Save(&ChildSession{
		Spec: Spec{
			AgentID: "child-cache",
			Task:    "inspect code",
			Limits:  Limits{MaxTurns: 5, OutputLimitTokens: 50},
		},
		Request: agent.RunRequest{
			Prompt: promptWithConversation("initial task"),
			Limits: agent.Limits{MaxTurns: 5, MaxTokens: 50},
		},
		Conversation: []agent.Message{
			{Role: agent.MessageRoleUser, Content: "initial task"},
			{Role: agent.MessageRoleAssistant, Content: "first answer"},
		},
		TurnCount:     1,
		TokenCount:    10,
		ToolCallCount: 0,
		TokenUsage:    TokenUsage{InputTokens: 100, CacheReadTokens: 900, CacheCreateTokens: 0, OutputTokens: 10},
	})

	var capturedEvent *output.DelegationCompleteEvent
	handler := NewFollowUpHandler(SubAgentHandlerDeps{
		SubAgentCfg:  config.SubAgentConfig{MaxTurns: 5, MaxTokens: 50},
		SessionStore: store,
		Events: output.SinkFunc(func(ev output.Event) {
			if ev.Type == output.EventTypeDelegationComplete {
				complete, ok := ev.Payload.(output.DelegationCompleteEvent)
				if ok {
					capturedEvent = &complete
				}
			}
		}),
		Runner: &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
			return agent.RunState{
				Conversation: []agent.Message{
					{Role: agent.MessageRoleUser, Content: "initial task"},
					{Role: agent.MessageRoleAssistant, Content: "second answer"},
				},
				TurnCount:         1,
				TokenCount:        5,
				InputTokens:       20,
				CacheReadTokens:   5,
				CacheCreateTokens: 0,
				StopReason:        agent.StopReasonComplete,
			}, nil
		}},
	})

	got, err := handler(context.Background(), map[string]any{
		"agent_id": "child-cache",
		"message":  "continue",
	})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	result := got.(tool.ExecutionResult).Value.(Result)
	if result.InputTokens != 120 {
		t.Fatalf("InputTokens=%d, want 120 (prior 100 + run 20)", result.InputTokens)
	}
	if result.CacheReadTokens != 905 {
		t.Fatalf("CacheReadTokens=%d, want 905 (prior 900 + run 5)", result.CacheReadTokens)
	}
	if result.CacheCreateTokens != 0 {
		t.Fatalf("CacheCreateTokens=%d, want 0", result.CacheCreateTokens)
	}

	if capturedEvent == nil {
		t.Fatal("DelegationCompleteEvent was not emitted")
	}
	if capturedEvent.InputTokens != 120 {
		t.Fatalf("event InputTokens=%d, want 120", capturedEvent.InputTokens)
	}
	if capturedEvent.CacheReadTokens != 905 {
		t.Fatalf("event CacheReadTokens=%d, want 905", capturedEvent.CacheReadTokens)
	}
	if capturedEvent.CacheCreateTokens != 0 {
		t.Fatalf("event CacheCreateTokens=%d, want 0", capturedEvent.CacheCreateTokens)
	}

	session, ok := store.Get("child-cache")
	if !ok {
		t.Fatal("session missing after follow-up")
	}
	wantUsage := TokenUsage{InputTokens: 120, CacheReadTokens: 905, CacheCreateTokens: 0, OutputTokens: 15}
	if session.TokenUsage != wantUsage {
		t.Fatalf("stored TokenUsage=%+v, want %+v", session.TokenUsage, wantUsage)
	}
}

func TestFollowUpHandler_ReusesOriginalProjectRoot(t *testing.T) {
	// Verify that follow_up reuses the original session's ProjectRoot,
	// which was set from the provisioned worktree path in the original
	// delegate call. This ensures the follow-up call operates in the same
	// isolated worktree, not a fresh location.

	originalProjectRoot := "/tmp/.steiner/worktrees/original-agent"

	store := NewSessionStore()
	store.Save(&ChildSession{
		Spec: Spec{
			AgentID: "code-agent",
			Task:    "implement feature",
		},
		Request: agent.RunRequest{
			Prompt: prompt.AssemblyOptions{
				ProjectRoot:  originalProjectRoot,
				Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "initial task"}},
			},
			Limits: agent.Limits{MaxTurns: 2, MaxTokens: 100},
		},
		Conversation: []agent.Message{
			{Role: agent.MessageRoleUser, Content: "initial task"},
			{Role: agent.MessageRoleAssistant, Content: "first answer"},
		},
		TurnCount:  1,
		TokenCount: 50,
	})

	var capturedReq agent.RunRequest
	handler := NewFollowUpHandler(SubAgentHandlerDeps{
		SubAgentCfg:  config.SubAgentConfig{MaxTurns: 5, MaxTokens: 100},
		SessionStore: store,
		Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			capturedReq = req
			return agent.RunState{
				Conversation: []agent.Message{
					{Role: agent.MessageRoleUser, Content: "initial task"},
					{Role: agent.MessageRoleAssistant, Content: "first answer"},
					{Role: agent.MessageRoleUser, Content: "continue"},
					{Role: agent.MessageRoleAssistant, Content: "second answer"},
				},
				TurnCount:  2,
				TokenCount: 75,
				StopReason: agent.StopReasonComplete,
			}, nil
		}},
	})

	_, err := handler(context.Background(), map[string]any{
		"agent_id": "code-agent",
		"message":  "continue with more detail",
	})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	// The follow-up request should preserve the original ProjectRoot from the session.
	if capturedReq.Prompt.ProjectRoot != originalProjectRoot {
		t.Errorf("follow-up ProjectRoot = %q, want %q (original worktree path)",
			capturedReq.Prompt.ProjectRoot, originalProjectRoot)
	}
}
