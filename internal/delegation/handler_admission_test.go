package delegation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func TestHandlerAdmissionMetadataForAsyncDelegates(t *testing.T) {
	for _, typ := range []AgentType{AgentTypeExplore, AgentTypeVision} {
		t.Run(string(typ), func(t *testing.T) {
			gate := make(chan struct{})
			deps := minimalDeps(&mockRunner{runFunc: func(ctx context.Context, _ agent.RunRequest) (agent.RunState, error) {
				select {
				case <-gate:
				case <-ctx.Done():
				}
				return successRunState(), nil
			}})
			deps.AsyncSubAgents = true
			sup, _ := newAsyncSupervisor(1, nil)
			blockerStarted, blockerRelease := make(chan struct{}), make(chan struct{})
			if _, _, err := sup.Spawn(context.Background(), ChildJob{AgentID: "blocker-" + string(typ), Execute: func(context.Context) (tool.ExecutionResult, error) {
				close(blockerStarted)
				<-blockerRelease
				return tool.ExecutionResult{}, nil
			}}); err != nil {
				t.Fatal(err)
			}
			<-blockerStarted
			deps.Supervisor = sup
			input := subAgentTask(typ, "inspect")
			input["group"] = " handler-group "
			if typ == AgentTypeVision {
				root := t.TempDir()
				imagePath := filepath.Join(root, "image.png")
				if err := os.WriteFile(imagePath, []byte("image"), 0o600); err != nil {
					t.Fatal(err)
				}
				deps.ImageStore = agent.NewImageStore(root)
				input["image_id"] = deps.ImageStore.Register(imagePath, "image/png", 1, 1, 5).ID
			}
			ctx := agent.WithToolBatchID(context.Background(), "batch-real-handler")
			got, err := SubAgentToolDef(deps, nil).Handler(ctx, input)
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			result, ok := got.(tool.ExecutionResult)
			if !ok {
				t.Fatalf("handler result = %T, want tool.ExecutionResult", got)
			}
			ack, ok := result.Value.(AckResult)
			if !ok {
				t.Fatalf("result value = %T, want AckResult", result.Value)
			}
			admission := result.DelegationAdmission
			if admission == nil || admission.Status != tool.DelegationAdmissionAccepted || admission.BatchID != "batch-real-handler" || admission.Group != "handler-group" || admission.AgentID != ack.AgentID || ack.AgentID == "" || !ack.Queued {
				t.Fatalf("ack = %+v, admission = %+v", ack, admission)
			}
			projection := ack.ProjectToolResult()
			if projection.Status != "queued" || projection.Continuation == nil || projection.Continuation.AgentID != ack.AgentID || projection.Output == "" {
				t.Fatalf("ack projection = %+v", projection)
			}
			close(gate)
			close(blockerRelease)
			sup.Shutdown(context.Background(), CancelCauseSystem)
		})
	}
}

func TestHandlerAdmissionValidationRejectsThroughExecutor(t *testing.T) {
	deps := minimalDeps(nil)
	deps.WorkDir = t.TempDir()
	deps.ImageStore = agent.NewImageStore(t.TempDir())
	deps.AgentModels = map[string]string{string(AgentTypeExplore): "unavailable"}
	deps.ModelResolver = func(string) (provider.Provider, provider.ResolvedModel, error) {
		return nil, provider.ResolvedModel{}, errors.New("model unavailable")
	}
	def := SubAgentToolDef(deps, []AgentType{AgentTypeResearch})
	executor := tool.NewExecutor(tool.NewRegistry(def), config.Config{}, nil, t.TempDir(), "", tool.Unsandboxed{})
	for _, test := range []struct {
		name  string
		input map[string]any
		want  string
	}{
		{name: "invalid type", input: subAgentTask(AgentType("invalid"), "inspect"), want: "unknown or unavailable type"},
		{name: "unavailable type", input: subAgentTask(AgentTypeResearch, "inspect"), want: `type "research" is unavailable`},
		{name: "invalid brief", input: map[string]any{"type": string(AgentTypeCode), "objective": "", "context": "valid", "deliverable": "valid", "constraints": []any{}, "success_criteria": []any{}, "checks": []any{}}, want: "objective is required"},
		{name: "missing vision image", input: subAgentTask(AgentTypeVision, "inspect"), want: "image_id is missing or empty"},
		{name: "unavailable vision image", input: func() map[string]any {
			input := subAgentTask(AgentTypeVision, "inspect")
			input["image_id"] = "missing-image"
			return input
		}(), want: "not registered in this conversation"},
		{name: "model resolution", input: subAgentTask(AgentTypeExplore, "inspect"), want: "model unavailable"},
		{name: "code feasibility", input: subAgentTask(AgentTypeCode, "inspect"), want: "provision code worktree"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := executor.Execute(context.Background(), SubAgentToolName, "call", test.input)
			if err == nil {
				t.Fatal("Execute returned nil error")
			}
			var carrier tool.DelegationAdmissionCarrier
			if !errors.As(err, &carrier) {
				t.Fatalf("error %T %v does not carry admission", err, err)
			}
			admission := carrier.DelegationAdmissionMetadata()
			if admission == nil || admission.Status != tool.DelegationAdmissionRejected {
				t.Fatalf("admission = %+v, want rejected", admission)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want marker %q", err, test.want)
			}
			if test.name == "code feasibility" {
				var setupErr *SetupError
				if !errors.As(err, &setupErr) {
					t.Fatalf("error %T does not retain SetupError", err)
				}
				projection := setupErr.ProjectToolError()
				if projection.Status != "failed" || projection.Output != "" || projection.Reason != "child setup failed" {
					t.Fatalf("setup projection = %+v", projection)
				}
			}
		})
	}
}

func TestHandlerGroupReuseRejectsWithTypedCorrectiveSetupError(t *testing.T) {
	deps := minimalDeps(&mockRunner{runFunc: func(context.Context, agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})
	deps.Supervisor = NewSupervisor(SupervisorOptions{MaxParallel: 1})
	input := subAgentTask(AgentTypeExplore, "inspect")
	input["group"] = " reused-group "
	def := SubAgentToolDef(deps, nil)
	if _, err := def.Handler(batchCtx("first-batch"), input); err != nil {
		t.Fatalf("first handler call: %v", err)
	}
	_, err := def.Handler(batchCtx("second-batch"), input)
	if err == nil {
		t.Fatal("second handler call returned nil error")
	}
	var carrier tool.DelegationAdmissionCarrier
	if !errors.As(err, &carrier) {
		t.Fatalf("error %T does not carry admission", err)
	}
	admission := carrier.DelegationAdmissionMetadata()
	if admission == nil || admission.Status != tool.DelegationAdmissionRejected || admission.BatchID != "second-batch" || admission.Group != "reused-group" {
		t.Fatalf("admission = %+v, want rejected reused-group admission", admission)
	}
	var setupErr *SetupError
	if !errors.As(err, &setupErr) {
		t.Fatalf("error %T does not retain SetupError", err)
	}
	var reservationErr *groupReservationError
	if !errors.As(err, &reservationErr) || !errors.Is(err, reservationErr) || reservationErr.name != "reused-group" {
		t.Fatalf("error %T does not retain typed reservation cause", err)
	}
	projection := setupErr.ProjectToolError()
	want := `delegation group name "reused-group" was already used; choose a fresh name`
	if projection.Status != "failed" || projection.Output != "" || projection.Reason != want {
		t.Fatalf("setup projection = %+v, want reason %q", projection, want)
	}
}

func TestBlockingHandlerAcceptedExecutionFailureKeepsMetadata(t *testing.T) {
	failure := errors.New("runner execution failed")
	deps := minimalDeps(&mockRunner{runFunc: func(context.Context, agent.RunRequest) (agent.RunState, error) { return agent.RunState{}, failure }})
	sup, sink := newAsyncSupervisor(1, nil)
	deps.Supervisor = sup
	ctx := agent.WithToolBatchID(context.Background(), "blocking-failure-batch")
	input := subAgentTask(AgentTypeExplore, "fail")
	input["group"] = " failure-group "
	got, err := SubAgentToolDef(deps, nil).Handler(ctx, input)
	if err != nil {
		t.Fatalf("handler error = %v, runner failures should return a failed result", err)
	}
	result, ok := got.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("handler result = %T, want tool.ExecutionResult", got)
	}
	value, ok := result.Value.(Result)
	if !ok || value.Status != StatusFailed || !strings.Contains(value.Reason, failure.Error()) {
		t.Fatalf("failure result = %#v, want failed result with runner error", result.Value)
	}
	admission := result.DelegationAdmission
	if admission == nil || admission.Status != tool.DelegationAdmissionAccepted || admission.BatchID != "blocking-failure-batch" || admission.Group != "failure-group" || admission.AgentID == "" || admission.AgentID != value.AgentID {
		t.Fatalf("failure admission = %+v, want accepted metadata for result %+v", admission, value)
	}
	sink.none(t)
}

func TestBlockingHandlerAcceptedPreparationFailureKeepsMetadata(t *testing.T) {
	deps := minimalDeps(&mockRunner{runFunc: func(context.Context, agent.RunRequest) (agent.RunState, error) {
		t.Fatal("runner called after preparation failure")
		return agent.RunState{}, nil
	}})
	deps.WorkDir = setupTestRepo(t)
	sup, sink := newAsyncSupervisor(1, nil)
	deps.Supervisor = sup
	if err := os.Mkdir(filepath.Join(deps.WorkDir, ".steiner"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deps.WorkDir, ".steiner", "worktrees"), []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := agent.WithToolBatchID(context.Background(), "blocking-prepare-batch")
	input := subAgentTask(AgentTypeCode, "prepare")
	input["group"] = " prepare-group "
	got, err := SubAgentToolDef(deps, nil).Handler(ctx, input)
	if err == nil || !strings.Contains(err.Error(), "provision code worktree") {
		t.Fatalf("handler error = %v, want worktree preparation failure", err)
	}
	var carrier tool.DelegationAdmissionCarrier
	if !errors.As(err, &carrier) {
		t.Fatalf("error %T lacks admission carrier", err)
	}
	if got != nil {
		t.Fatalf("handler result = %#v, want nil on error", got)
	}
	admission := carrier.DelegationAdmissionMetadata()
	if admission == nil || admission.Status != tool.DelegationAdmissionAccepted || admission.BatchID != "blocking-prepare-batch" || admission.Group != "prepare-group" || admission.AgentID == "" {
		t.Fatalf("preparation admission = %+v", admission)
	}
	var setupErr *SetupError
	if !errors.As(err, &setupErr) {
		t.Fatalf("error %T does not retain SetupError", err)
	}
	projection := setupErr.ProjectToolError()
	if projection.Status != "failed" || projection.Output != "" || projection.Reason != "child setup failed" {
		t.Fatalf("setup projection = %+v", projection)
	}
	sink.none(t)
}

func TestBlockingHandlerKeepsAcceptedAdmissionAndDoesNotPost(t *testing.T) {
	deps := minimalDeps(&mockRunner{runFunc: func(context.Context, agent.RunRequest) (agent.RunState, error) { return successRunState(), nil }})
	sup, sink := newAsyncSupervisor(1, nil)
	deps.Supervisor = sup
	ctx := agent.WithToolBatchID(context.Background(), "blocking-batch")
	input := subAgentTask(AgentTypeExplore, "inspect")
	input["group"] = " blocking-group "
	got, err := SubAgentToolDef(deps, nil).Handler(ctx, input)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	result, ok := got.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("handler result = %T, want tool.ExecutionResult", got)
	}
	value, ok := result.Value.(Result)
	if !ok {
		t.Fatalf("result value = %T, want blocking Result", result.Value)
	}
	admission := result.DelegationAdmission
	if admission == nil || admission.Status != tool.DelegationAdmissionAccepted || admission.BatchID != "blocking-batch" || admission.Group != "blocking-group" || admission.AgentID == "" || admission.AgentID != value.AgentID {
		t.Fatalf("result = %+v, admission = %+v", value, admission)
	}
	sink.none(t)
}
