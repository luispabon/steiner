package delegation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

func TestAgentTypeDocumentorDefinition(t *testing.T) {
	t.Parallel()

	if !ValidAgentType("documentor") {
		t.Fatal("ValidAgentType(\"documentor\") = false, want true")
	}
	if !slices.Contains(AllAgentTypes(), AgentTypeDocumentor) {
		t.Fatal("AgentTypeDocumentor missing from AllAgentTypes")
	}
	if got, want := AgentAllowedTools(AgentTypeDocumentor), AgentAllowedTools(AgentTypeCode); !slices.Equal(got, want) {
		t.Fatalf("AgentAllowedTools(documentor) = %v, want code's allowlist %v", got, want)
	}
	if got := AgentSystemPrompt(AgentTypeDocumentor); got != "" {
		t.Fatalf("AgentSystemPrompt(documentor) = %q, want empty (the prompt package renders its duties)", got)
	}
	if got := AgentSystemSuffix(AgentTypeDocumentor, false, false); got != "" {
		t.Fatalf("AgentSystemSuffix(documentor, false, false) = %q, want empty", got)
	}
	combined := AgentSystemSuffix(AgentTypeDocumentor, true, true)
	for _, want := range []string{"exactly ONE advisor call", "## Code intelligence (LSP)"} {
		if !strings.Contains(combined, want) {
			t.Errorf("AgentSystemSuffix(documentor, true, true) missing %q", want)
		}
	}
}

func TestAgentTypeDocumentorDescription(t *testing.T) {
	t.Parallel()
	want := "Spawn a documentation sub-agent to write or update documentation files (Markdown, READMEs, user and internals docs) in an isolated worktree."
	if got := specializedDescription(AgentTypeDocumentor); got != want {
		t.Fatalf("specializedDescription(documentor) = %q, want %q", got, want)
	}
}

func TestUsesWorktree(t *testing.T) {
	t.Parallel()
	for _, agentType := range AllAgentTypes() {
		want := agentType == AgentTypeCode || agentType == AgentTypeDocumentor
		if got := UsesWorktree(agentType); got != want {
			t.Errorf("UsesWorktree(%q) = %v, want %v", agentType, got, want)
		}
	}
}

func TestAgentWriteScopeDocumentor(t *testing.T) {
	t.Parallel()
	scope, ok := agentWriteScope(AgentTypeDocumentor)
	if !ok {
		t.Fatal("agentWriteScope(documentor) reported no scope")
	}
	wantReason := "documentor: writes are restricted to documentation files (.md, .mdx, .markdown, .rst, .adoc, README, CHANGELOG, CONTRIBUTING). If the task needs other files, stop and report which files and why."
	if scope.Reason != wantReason {
		t.Fatalf("scope reason = %q, want %q", scope.Reason, wantReason)
	}
	if want := []string{".md", ".mdx", ".markdown", ".rst", ".adoc"}; !slices.Equal(scope.Extensions, want) {
		t.Errorf("scope extensions = %v, want %v", scope.Extensions, want)
	}
	if want := []string{"README", "CHANGELOG", "CONTRIBUTING"}; !slices.Equal(scope.Basenames, want) {
		t.Errorf("scope basenames = %v, want %v", scope.Basenames, want)
	}
	for _, agentType := range AllAgentTypes() {
		if agentType == AgentTypeDocumentor {
			continue
		}
		if _, ok := agentWriteScope(agentType); ok {
			t.Errorf("agentWriteScope(%q) reported a scope, want none", agentType)
		}
	}
}

func TestDocumentorPlanModeDispatchDenied(t *testing.T) {
	t.Parallel()
	ctx := context.WithValue(context.Background(), tool.ExecutionModeKey{}, config.ExecutionModePlan)
	tests := []struct {
		agentType AgentType
		wantErr   string
	}{
		{agentType: AgentTypeCode, wantErr: "code: plan mode is active; the code sub-agent (which can mutate files) is unavailable"},
		{agentType: AgentTypeDocumentor, wantErr: "documentor: plan mode is active; the documentor sub-agent (which can mutate files) is unavailable"},
	}
	for _, tt := range tests {
		t.Run(string(tt.agentType), func(t *testing.T) {
			runCalled := false
			runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
				runCalled = true
				return successRunState(), nil
			}}
			_, err := newSpecializedHandler(tt.agentType, minimalDeps(runner))(ctx, validStructuredTask("t"))
			if err == nil {
				t.Fatal("expected plan-mode denial")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
			if runCalled {
				t.Fatal("runner was invoked despite the plan-mode denial")
			}
		})
	}

	t.Run("read-only types stay available", func(t *testing.T) {
		runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
			return successRunState(), nil
		}}
		if _, err := newSpecializedHandler(AgentTypeExplore, minimalDeps(runner))(ctx, validStructuredTask("t")); err != nil {
			t.Fatalf("explore in plan mode error = %v, want nil", err)
		}
	})
}

// documentorTestDeps builds handler deps whose parent registry exposes the real
// mutate tool, so the child executor's write scope is exercised end to end.
func documentorTestDeps(runner AgentRunner, workDir string, store *SessionStore) SpecializedToolDeps {
	return SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg:  config.SubAgentConfig{MaxFollowUps: 100, MaxParallel: 1},
			Provider:     stubProvider{},
			ParentReg:    tool.NewRegistry(builtin.NewMutateTool(builtin.Env{WorkDir: workDir})),
			Runner:       runner,
			Events:       noopEventSink{},
			WorkDir:      workDir,
			SessionStore: store,
			Sandbox:      tool.Unsandboxed{},
		},
	}
}

func executeChildMutate(exec agent.ToolExecutor, path string) error {
	_, err := exec.Execute(context.Background(), "mutate", "call-mutate", map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": path, "content": "content\n"},
		},
	})
	return err
}

// documentorTaskInput builds a structured documentor task input from its three
// required text fields.
func documentorTaskInput(objective, taskContext, deliverable string) map[string]any {
	return map[string]any{
		"type":             string(AgentTypeDocumentor),
		"objective":        objective,
		"context":          taskContext,
		"deliverable":      deliverable,
		"constraints":      []any{},
		"success_criteria": []any{},
		"checks":           []any{},
	}
}

func assertDocumentorScopeDenied(t *testing.T, err error, path string) {
	t.Helper()
	if err == nil {
		t.Fatalf("mutate(%q) error = nil, want policy_denied", path)
	}
	var toolErr *tool.ToolExecutionError
	if !errors.As(err, &toolErr) || toolErr.Kind != "policy_denied" {
		t.Fatalf("mutate(%q) error = %v, want ToolExecutionError{Kind: policy_denied}", path, err)
	}
	if !strings.Contains(toolErr.Message, documentorWriteScope.Reason) {
		t.Fatalf("mutate(%q) message = %q, want the documentor scope reason", path, toolErr.Message)
	}
}

func TestDocumentorInitialRunProvisionsWorktreeAndScopesWrites(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t)
	store := NewSessionStore()

	var capturedReq agent.RunRequest
	captured := false
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		if !captured {
			capturedReq = req
			captured = true
		}
		return successRunState(), nil
	}}
	deps := documentorTestDeps(runner, repo, store)

	raw, err := SubAgentToolDef(deps, nil).Handler(context.Background(), subAgentTask(AgentTypeDocumentor, "write the docs"))
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
	if delegationResult.WorktreePath == "" || delegationResult.WorktreeBranch == "" {
		t.Fatalf("worktree fields = (%q, %q), want path and branch", delegationResult.WorktreePath, delegationResult.WorktreeBranch)
	}
	if capturedReq.Prompt.ProjectRoot != delegationResult.WorktreePath {
		t.Fatalf("child ProjectRoot = %q, want worktree %q", capturedReq.Prompt.ProjectRoot, delegationResult.WorktreePath)
	}

	if err := os.MkdirAll(filepath.Join(delegationResult.WorktreePath, "docs"), 0o755); err != nil {
		t.Fatalf("create docs dir: %v", err)
	}
	for _, path := range []string{"docs/guide.md", "README", "CHANGELOG.md", "notes.rst", "guide.adoc", "page.mdx", "page.markdown"} {
		if err := executeChildMutate(capturedReq.Executor, path); err != nil {
			t.Errorf("mutate(%q) error = %v, want allowed", path, err)
			continue
		}
		if _, err := os.Stat(filepath.Join(delegationResult.WorktreePath, path)); err != nil {
			t.Errorf("allowed write %q is missing from the worktree: %v", path, err)
		}
		if _, err := os.Stat(filepath.Join(repo, path)); !os.IsNotExist(err) {
			t.Errorf("allowed write %q reached the parent repository: stat err = %v", path, err)
		}
	}
	for _, path := range []string{"main.go", "notes.txt", "docs.mdx/secret.go"} {
		err := executeChildMutate(capturedReq.Executor, path)
		assertDocumentorScopeDenied(t, err, path)
	}
	if _, err := os.Stat(filepath.Join(delegationResult.WorktreePath, "main.go")); !os.IsNotExist(err) {
		t.Fatalf("denied write created main.go: stat err = %v", err)
	}

	// A parent-checkout file stays out of reach even though its name is allowed:
	// the scope is anchored to the child's worktree root, so an absolute path
	// into the parent repository is denied and the parent file is untouched.
	parentReadme := filepath.Join(repo, "README.md")
	if err := os.WriteFile(parentReadme, []byte("parent\n"), 0o644); err != nil {
		t.Fatalf("write parent README: %v", err)
	}
	assertDocumentorScopeDenied(t, executeChildMutate(capturedReq.Executor, parentReadme), parentReadme)
	if content, err := os.ReadFile(parentReadme); err != nil || string(content) != "parent\n" {
		t.Fatalf("parent README changed: content=%q err=%v, want unchanged", content, err)
	}

	session, ok := store.Get(delegationResult.AgentID)
	if !ok {
		t.Fatalf("child session for %q not saved", delegationResult.AgentID)
	}
	assertDocumentorScopeDenied(t, executeChildMutate(session.Request.Executor, "tool.go"), "tool.go")
}

// TestSpecializedHandler_DocumentorEmptyTask covers the structured-task
// validation that TestSpecializedHandler_EmptyTask skips for worktree-isolated
// types, which need a git repository.
func TestSpecializedHandler_DocumentorEmptyTask(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t)
	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}}
	def := SubAgentToolDef(documentorTestDeps(runner, repo, nil), nil)

	tests := []struct {
		name    string
		input   map[string]any
		wantErr string
	}{
		{
			name:    "missing fields",
			input:   map[string]any{"type": string(AgentTypeDocumentor)},
			wantErr: "objective",
		},
		{
			name:    "empty objective",
			input:   documentorTaskInput("", "context", "deliverable"),
			wantErr: "objective",
		},
		{
			name:    "blank objective",
			input:   documentorTaskInput("   ", "context", "deliverable"),
			wantErr: "objective",
		},
		{
			name:    "empty context",
			input:   documentorTaskInput("objective", "", "deliverable"),
			wantErr: "context",
		},
		{
			name:    "empty deliverable",
			input:   documentorTaskInput("objective", "context", ""),
			wantErr: "deliverable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := def.Handler(context.Background(), tt.input)
			if err == nil {
				t.Fatalf("handler error = nil, want a validation error mentioning %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
			if raw != nil {
				t.Fatalf("result = %#v, want nil on a validation failure", raw)
			}
		})
	}

	t.Run("valid task provisions a worktree", func(t *testing.T) {
		raw, err := def.Handler(context.Background(), subAgentTask(AgentTypeDocumentor, "write the docs"))
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
			t.Fatal("WorktreePath is empty, want a provisioned worktree")
		}
	})
}

// TestDocumentorWriteScopeSurvivesRelaxedPathConfig proves the documentor write
// scope is enforced inside the child executor regardless of a permissive
// project path policy: neither a non-documentation file in the worktree nor a
// documentation file outside it may be written.
func TestDocumentorWriteScopeSurvivesRelaxedPathConfig(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t)
	outside := t.TempDir()

	var capturedReq agent.RunRequest
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		capturedReq = req
		return successRunState(), nil
	}}
	deps := documentorTestDeps(runner, repo, nil)
	deps.ExecutorConfig = tool.ExecutorConfigFromConfig(config.Config{Paths: config.PathsConfig{
		ProjectRootOnly: false,
		WritablePaths:   []string{outside},
	}})

	if _, err := SubAgentToolDef(deps, nil).Handler(context.Background(), subAgentTask(AgentTypeDocumentor, "write the docs")); err != nil {
		t.Fatalf("handler error: %v", err)
	}

	assertDocumentorScopeDenied(t, executeChildMutate(capturedReq.Executor, "main.go"), "main.go")
	outsideDoc := filepath.Join(outside, "guide.md")
	assertDocumentorScopeDenied(t, executeChildMutate(capturedReq.Executor, outsideDoc), outsideDoc)
	if _, err := os.Stat(outsideDoc); !os.IsNotExist(err) {
		t.Fatalf("denied out-of-worktree write created %q: stat err = %v", outsideDoc, err)
	}
}

func TestCodeChildHasNoWriteScope(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t)
	var capturedReq agent.RunRequest
	captured := false
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		if !captured {
			capturedReq = req
			captured = true
		}
		return successRunState(), nil
	}}
	deps := documentorTestDeps(runner, repo, nil)

	if _, err := SubAgentToolDef(deps, nil).Handler(context.Background(), subAgentTask(AgentTypeCode, "implement")); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if err := executeChildMutate(capturedReq.Executor, "main.go"); err != nil {
		t.Fatalf("code child mutate(main.go) error = %v, want nil", err)
	}
}

func TestDocumentorFollowUpRetainsWriteScope(t *testing.T) {
	const agentID = "documentor-followup-scope"
	originalIDGen := idGen
	idGen = func() string { return agentID }
	t.Cleanup(func() { idGen = originalIDGen })

	repo := setupTestRepo(t)
	store := NewSessionStore()
	deps := documentorTestDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}}, repo, store)

	if _, err := newSpecializedHandler(AgentTypeDocumentor, deps)(context.Background(), validStructuredTask("write docs")); err != nil {
		t.Fatalf("initial delegation error: %v", err)
	}

	var followUpReq agent.RunRequest
	followUp := NewFollowUpHandler(SubAgentHandlerDeps{
		SubAgentCfg:  config.SubAgentConfig{MaxFollowUps: 100},
		SessionStore: store,
		Events:       noopEventSink{},
		Sandbox:      tool.Unsandboxed{},
		Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			followUpReq = req
			return successRunState(), nil
		}},
	})
	if _, err := followUp(context.Background(), map[string]any{"agent_id": agentID, "message": "continue"}); err != nil {
		t.Fatalf("follow-up error: %v", err)
	}

	assertDocumentorScopeDenied(t, executeChildMutate(followUpReq.Executor, "main.go"), "main.go")
	if err := executeChildMutate(followUpReq.Executor, "CHANGELOG"); err != nil {
		t.Fatalf("follow-up allowed documentor write error = %v, want nil", err)
	}
}

func TestDocumentorRemediationRetainsWriteScope(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t)
	store := NewSessionStore()

	var remediationReq agent.RunRequest
	calls := 0
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		calls++
		if calls > 1 {
			remediationReq = req
			return successRunState(), nil
		}
		// Leave the worktree dirty so commit remediation runs and reuses the
		// initial run request, including its scoped executor.
		if err := os.WriteFile(filepath.Join(req.Prompt.ProjectRoot, "README.md"), []byte("dirty\n"), 0o644); err != nil {
			t.Errorf("write dirty doc: %v", err)
		}
		return successRunState(), nil
	}}
	deps := documentorTestDeps(runner, repo, store)

	if _, err := SubAgentToolDef(deps, nil).Handler(context.Background(), subAgentTask(AgentTypeDocumentor, "write docs")); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if calls < 2 {
		t.Fatalf("runner calls = %d, want a commit-remediation run", calls)
	}
	assertDocumentorScopeDenied(t, executeChildMutate(remediationReq.Executor, "main.go"), "main.go")
}

func TestDocumentorFollowUpPlanModeDenied(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t)
	worktree, err := ProvisionCodeWorktree(context.Background(), repo, "documentor-plan-mode")
	if err != nil {
		t.Fatalf("provision worktree: %v", err)
	}

	store := NewSessionStore()
	store.Save(&ChildSession{
		Spec:    Spec{AgentID: "documentor-plan-mode", AgentType: AgentTypeDocumentor, Task: "write docs"},
		Request: agent.RunRequest{Tools: []provider.ToolSpec{{Function: provider.ToolFunctionSpec{Name: "mutate"}}}},
		Remediation: &RemediationConfig{
			WorktreePath:   worktree.Path,
			ExpectedBranch: worktree.Branch,
		},
	})

	runCalled := false
	handler := NewFollowUpHandler(SubAgentHandlerDeps{
		SubAgentCfg:  config.SubAgentConfig{MaxFollowUps: 100},
		SessionStore: store,
		Runner: &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
			runCalled = true
			return successRunState(), nil
		}},
	})
	ctx := context.WithValue(context.Background(), tool.ExecutionModeKey{}, config.ExecutionModePlan)
	_, err = handler(ctx, map[string]any{"agent_id": "documentor-plan-mode", "message": "continue"})
	if err == nil {
		t.Fatal("expected plan-mode follow-up denial")
	}
	if !strings.Contains(err.Error(), "sub-agents that can mutate files are unavailable") {
		t.Fatalf("error = %q, want the type-neutral plan-mode denial", err.Error())
	}
	if runCalled {
		t.Fatal("runner was invoked despite the plan-mode denial")
	}
}

func TestDocumentorCancellationDiscardsWorktree(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t)
	worktree, err := ProvisionCodeWorktree(context.Background(), repo, "documentor-cancel")
	if err != nil {
		t.Fatalf("provision worktree: %v", err)
	}
	controller := NewActiveController()
	if _, err := registerChild(controller, "documentor-cancel", context.Background(), AgentTypeDocumentor, worktree); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := controller.CancelAgentWithDiscard("documentor-cancel", true); got != CancelAccepted {
		t.Fatalf("cancellation with discard = %v, want %v", got, CancelAccepted)
	}
	store := NewSessionStore()
	store.Save(&ChildSession{Spec: Spec{AgentID: "documentor-cancel"}})
	events := &recordingEventSink{}
	result := Result{
		AgentID:          "documentor-cancel",
		Status:           StatusPartial,
		SessionResumable: true,
		Output:           "work; " + cancelledSessionRetentionPhrase,
	}

	finalizeDelegateCancellation(events, store, controller, repo, "documentor-cancel", &result)

	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("discarded worktree stat = %v, want not exist", err)
	}
	if !strings.Contains(result.Output, cancelledSessionDiscardNotice) {
		t.Fatalf("result output = %q, want the discard notice", result.Output)
	}
	disposals := disposalEvents(events.Events())
	if len(disposals) != 1 {
		t.Fatalf("disposal events = %d, want 1", len(disposals))
	}
	if got := disposals[0].Scope.AgentType; got != string(AgentTypeDocumentor) {
		t.Fatalf("disposal event agent type = %q, want %q", got, AgentTypeDocumentor)
	}
}
