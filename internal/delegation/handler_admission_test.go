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
			deps.Supervisor = sup
			input := subAgentTask(typ, "inspect")
			input["group"] = "  handler-group  "
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
			if admission == nil || admission.Status != tool.DelegationAdmissionAccepted || admission.BatchID != "batch-real-handler" || admission.Group != "handler-group" || admission.AgentID != ack.AgentID || ack.AgentID == "" || ack.Queued {
				t.Fatalf("ack = %+v, admission = %+v", ack, admission)
			}
			close(gate)
			sup.Shutdown(context.Background(), CancelCauseSystem)
		})
	}
}

func TestHandlerAdmissionValidationRejectsThroughExecutor(t *testing.T) {
	deps := minimalDeps(nil)
	def := SubAgentToolDef(deps, []AgentType{AgentTypeExplore})
	executor := tool.NewExecutor(tool.NewRegistry(def), config.Config{}, nil, t.TempDir(), "", tool.Unsandboxed{})
	for _, test := range []struct {
		name  string
		input map[string]any
	}{
		{name: "invalid type", input: subAgentTask(AgentType("invalid"), "inspect")},
		{name: "unavailable type", input: subAgentTask(AgentTypeExplore, "inspect")},
		{name: "invalid brief", input: map[string]any{"type": string(AgentTypeCode)}},
		{name: "missing vision image", input: subAgentTask(AgentTypeVision, "inspect")},
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
			if strings.TrimSpace(err.Error()) == "" {
				t.Fatal("error is empty")
			}
		})
	}
}

func TestBlockingHandlerKeepsAcceptedAdmissionAndDoesNotPost(t *testing.T) {
	deps := minimalDeps(&mockRunner{runFunc: func(context.Context, agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})
	sup, sink := newAsyncSupervisor(1, nil)
	deps.Supervisor = sup
	ctx := agent.WithToolBatchID(context.Background(), "blocking-batch")
	input := subAgentTask(AgentTypeExplore, "inspect")
	input["group"] = "  blocking-group  "
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
	if admission == nil || admission.Status != tool.DelegationAdmissionAccepted || admission.BatchID != "blocking-batch" || admission.Group != "blocking-group" || admission.AgentID != value.AgentID {
		t.Fatalf("result = %+v, admission = %+v", value, admission)
	}
	sink.none(t)
}
