package delegation

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/prompt"
)

func remediationTestSpec() Spec {
	return Spec{
		AgentID: "remediation-agent",
		Task:    "implement the task",
		Limits:  Limits{MaxTurns: 5, OutputLimitTokens: 1000, Timeout: time.Minute},
	}
}

func remediationTestRequest() agent.RunRequest {
	return agent.RunRequest{Prompt: prompt.AssemblyOptions{}}
}

func TestApplyRemediation(t *testing.T) {
	initialState := successRunState()
	originalOutput := "task result"
	tests := []struct {
		name            string
		state           agent.RunState
		cfg             *RemediationConfig
		wantOutcome     remediationOutcome
		wantStatus      Status
		wantOutput      string
		wantWarning     bool
		warningContains string
		wantRunnerCalls int
		wantError       bool
		wantResumable   bool
		runErr          error
	}{
		{
			name: "nil config", state: initialState, wantOutcome: remediationNotAttempted,
			wantStatus: StatusComplete, wantOutput: originalOutput,
		},
		{
			name:  "non-complete stop with dirty tree",
			state: agent.RunState{Conversation: initialState.Conversation, TurnCount: 1, TokenCount: 100, StopReason: agent.StopReasonError},
			cfg:   remediationConfigForDirty([]string{"changed.go"}, nil, nil), wantOutcome: remediationNotAttempted,
			wantStatus: StatusFailed, wantWarning: true,
		},
		{
			name: "complete and clean", state: initialState,
			cfg: remediationConfigForDirty(nil, nil, nil), wantOutcome: remediationNotAttempted,
			wantStatus: StatusComplete, wantOutput: originalOutput,
		},
		{
			name: "dirty and commits successfully", state: initialState,
			cfg:         remediationConfigForDirty([]string{"changed.go"}, []string{}, func() (bool, error) { return true, nil }),
			wantOutcome: remediationAttempted, wantStatus: StatusComplete,
			wantOutput: originalOutput + "\n\n<remediation note: committed remaining changes; worktree left clean>", wantRunnerCalls: 1,
		},
		{
			name: "dirty and commit verification fails", state: initialState,
			cfg:         remediationConfigForDirty([]string{"changed.go"}, []string{"changed.go"}, func() (bool, error) { return false, nil }),
			wantOutcome: remediationAttempted, wantStatus: StatusFailed, wantOutput: originalOutput,
			wantWarning: true, wantRunnerCalls: 1, wantError: true,
		},
		{
			name: "remediation run errors", state: initialState,
			cfg:         remediationConfigForDirty([]string{"changed.go"}, []string{}, nil),
			wantOutcome: remediationAttempted, wantStatus: StatusFailed, wantOutput: originalOutput,
			wantWarning: true, wantRunnerCalls: 1, wantError: true, runErr: errors.New("runner failed"),
		},
		{
			name: "clean tree but committed false", state: initialState,
			cfg:         remediationConfigForDirty([]string{"changed.go"}, []string{}, func() (bool, error) { return false, nil }),
			wantOutcome: remediationAttempted, wantStatus: StatusFailed, wantOutput: originalOutput,
			wantWarning: true, wantRunnerCalls: 1, wantError: true,
		},
		{
			name: "tree remains dirty", state: initialState,
			cfg:         remediationConfigForDirty([]string{"changed.go"}, []string{"changed.go"}, func() (bool, error) { return false, nil }),
			wantOutcome: remediationAttempted, wantStatus: StatusFailed, wantOutput: originalOutput,
			wantWarning: true, wantRunnerCalls: 1, wantError: true,
		},
		{
			name: "initial dirty check errors", state: initialState,
			cfg:         remediationConfigWithInitialError(errors.New("cannot inspect worktree")),
			wantOutcome: remediationAttempted, wantStatus: StatusFailed, wantOutput: originalOutput,
			wantWarning: true, warningContains: "could not verify", wantError: true,
		},
		{
			name: "pre-head check errors", state: initialState,
			cfg:         remediationConfigWithHeadError(errors.New("cannot read HEAD")),
			wantOutcome: remediationAttempted, wantStatus: StatusFailed, wantOutput: originalOutput,
			wantWarning: true, wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
				calls++
				state := successRunState()
				state.TurnCount = 2
				return state, tt.runErr
			}}
			gotState, _, result, outcome, err := applyRemediation(
				context.Background(), remediationTestSpec(), remediationTestRequest(), runner,
				tt.state, CacheUsage{}, tt.cfg, newTraceCollector("remediation-agent", "implement the task"),
			)
			if outcome != tt.wantOutcome {
				t.Errorf("outcome = %q, want %q", outcome, tt.wantOutcome)
			}
			if result.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", result.Status, tt.wantStatus)
			}
			if tt.wantOutput != "" && result.Output != tt.wantOutput {
				t.Errorf("output = %q, want %q", result.Output, tt.wantOutput)
			}
			if tt.warningContains != "" {
				if !containsWarningText(result.Warnings, tt.warningContains) {
					t.Errorf("warnings = %v, want warning containing %q", result.Warnings, tt.warningContains)
				}
			} else if tt.wantWarning && !containsWarning(result.Warnings) {
				t.Errorf("warnings = %v, want dirty-worktree warning", result.Warnings)
			}
			if calls != tt.wantRunnerCalls {
				t.Errorf("runner calls = %d, want %d", calls, tt.wantRunnerCalls)
			}
			if tt.wantRunnerCalls > 0 && gotState.TurnCount != 2 {
				t.Errorf("state turn count = %d, want 2", gotState.TurnCount)
			}
			if result.SessionResumable != tt.wantResumable {
				t.Errorf("SessionResumable = %t, want %t", result.SessionResumable, tt.wantResumable)
			}
			if (err != nil) != tt.wantError {
				t.Errorf("error present = %t, want %t; err=%v", err != nil, tt.wantError, err)
			}
		})
	}
}

func containsWarning(warnings []string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, "is not clean") {
			return true
		}
	}
	return false
}

func containsWarningText(warnings []string, text string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, text) {
			return true
		}
	}
	return false
}

func remediationConfigForDirty(initial, after []string, committed func() (bool, error)) *RemediationConfig {
	calls := 0
	return &RemediationConfig{
		WorktreePath: "/tmp/remediation-worktree", ExpectedBranch: "delegate/remediation",
		IsDirty: func(context.Context) ([]string, error) {
			calls++
			if calls == 1 {
				return initial, nil
			}
			return after, nil
		},
		Head: func(context.Context) (string, error) { return "head-before", nil },
		Committed: func(context.Context, string, []string) (bool, error) {
			if committed == nil {
				return true, nil
			}
			return committed()
		},
	}
}

func remediationConfigWithInitialError(err error) *RemediationConfig {
	return &RemediationConfig{
		WorktreePath: "/tmp/remediation-worktree",
		IsDirty:      func(context.Context) ([]string, error) { return nil, err },
		Head:         func(context.Context) (string, error) { return "", nil },
		Committed:    func(context.Context, string, []string) (bool, error) { return false, nil },
	}
}

func remediationConfigWithHeadError(err error) *RemediationConfig {
	return &RemediationConfig{
		WorktreePath: "/tmp/remediation-worktree",
		IsDirty:      func(context.Context) ([]string, error) { return []string{"changed.go"}, nil },
		Head:         func(context.Context) (string, error) { return "", err },
		Committed:    func(context.Context, string, []string) (bool, error) { return false, nil },
	}
}

func TestSpawnDelegate_RemediationSurvivesSummary(t *testing.T) {
	calls := 0
	remediationCalls := 0
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		calls++
		if _, ok := req.Executor.(summaryOnlyExecutor); ok {
			return agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "retained summary"}}, StopReason: agent.StopReasonComplete, TurnCount: 2, TokenCount: 200}, nil
		}
		if len(req.Prompt.Conversation) > 0 && strings.Contains(req.Prompt.Conversation[len(req.Prompt.Conversation)-1].Content, "Pre-remediation HEAD") {
			remediationCalls++
			return agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "committed changes"}}, StopReason: agent.StopReasonComplete, TurnCount: 2, TokenCount: 200}, nil
		}
		return successRunState(), nil
	}}
	isDirtyCalls := 0
	cfg := &RemediationConfig{
		WorktreePath: "/tmp/remediation-worktree", ExpectedBranch: "delegate/remediation",
		IsDirty: func(context.Context) ([]string, error) {
			isDirtyCalls++
			if isDirtyCalls == 1 {
				return []string{"changed.go"}, nil
			}
			return nil, nil
		},
		Head:      func(context.Context) (string, error) { return "head-before", nil },
		Committed: func(context.Context, string, []string) (bool, error) { return true, nil },
	}

	traceLogger, err := NewTraceLogger(filepath.Join(t.TempDir(), "trace.jsonl"))
	if err != nil {
		t.Fatalf("create trace logger: %v", err)
	}
	defer func() {
		if cerr := traceLogger.Close(); cerr != nil {
			t.Errorf("close trace logger: %v", cerr)
		}
	}()
	result, state, _, err := SpawnDelegate(context.Background(), remediationTestSpec(), remediationTestRequest(), runner, noopEventSink{}, traceLogger, WithRemediation(cfg))
	if err != nil {
		t.Fatalf("SpawnDelegate error: %v", err)
	}
	delegationResult := result.Value.(Result)
	if delegationResult.Status != StatusComplete {
		t.Errorf("status = %q, want %q", delegationResult.Status, StatusComplete)
	}
	if !strings.Contains(delegationResult.Output, "<remediation note: committed remaining changes; worktree left clean>") {
		t.Errorf("output = %q, missing remediation note", delegationResult.Output)
	}
	if delegationResult.TurnCount != 2 || delegationResult.TokenCount != 200 {
		t.Errorf("counts = (%d, %d), want (2, 200)", delegationResult.TurnCount, delegationResult.TokenCount)
	}
	if state.TurnCount != 2 || remediationCalls != 1 || calls != 3 {
		t.Errorf("state turn=%d, remediation calls=%d, total calls=%d; want 2, 1, 3", state.TurnCount, remediationCalls, calls)
	}
	if delegationResult.Summary != "retained summary" {
		t.Errorf("summary = %q, want retained summary", delegationResult.Summary)
	}
}

func TestSpawnDelegate_NonCompleteDirtySkipsRemediation(t *testing.T) {
	remediationCalls := 0
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		if _, ok := req.Executor.(summaryOnlyExecutor); ok {
			return agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "retained summary"}}, StopReason: agent.StopReasonComplete}, nil
		}
		if len(req.Prompt.Conversation) > 0 && strings.Contains(req.Prompt.Conversation[len(req.Prompt.Conversation)-1].Content, "Pre-remediation HEAD") {
			remediationCalls++
		}
		return agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "failed task"}}, StopReason: agent.StopReasonError, TurnCount: 1, TokenCount: 100}, nil
	}}
	cfg := &RemediationConfig{
		WorktreePath: "/tmp/remediation-worktree",
		IsDirty:      func(context.Context) ([]string, error) { return []string{"changed.go"}, nil },
	}

	result, _, _, err := SpawnDelegate(context.Background(), remediationTestSpec(), remediationTestRequest(), runner, noopEventSink{}, nil, WithRemediation(cfg))
	if err != nil {
		t.Fatalf("SpawnDelegate error: %v", err)
	}
	delegationResult := result.Value.(Result)
	if delegationResult.Status != StatusFailed {
		t.Errorf("status = %q, want %q", delegationResult.Status, StatusFailed)
	}
	if !containsWarning(delegationResult.Warnings) {
		t.Errorf("warnings = %v, want dirty-worktree warning", delegationResult.Warnings)
	}
	if remediationCalls != 0 {
		t.Errorf("remediation calls = %d, want 0", remediationCalls)
	}
}
