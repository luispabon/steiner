package delegation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

func TestSpecializedPreparationRunsAfterAcceptedEvent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		typ  AgentType
	}{
		{name: "non-code", typ: AgentTypeExplore},
		{name: "vision", typ: AgentTypeVision},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			accepted := make(chan struct{})
			release := make(chan struct{})
			deps := minimalDeps(&mockRunner{runFunc: func(context.Context, agent.RunRequest) (agent.RunState, error) {
				return successRunState(), nil
			}})
			deps.WorkDir = root
			deps.AsyncSubAgents = true
			deps.Events = acceptedBarrierSink(root, accepted, release, t)
			deps.Supervisor = NewSupervisor(SupervisorOptions{MaxParallel: 1, Events: deps.Events})
			input := subAgentTask(tt.typ, "inspect")
			if tt.typ == AgentTypeVision {
				imagePath := filepath.Join(root, "stored-image.png")
				if err := os.WriteFile(imagePath, []byte("image"), 0o600); err != nil {
					t.Fatal(err)
				}
				deps.ImageStore = agent.NewImageStore(root)
				ref := deps.ImageStore.Register(imagePath, "image/png", 1, 1, 5)
				input["image_id"] = ref.ID
			}
			done := make(chan error, 1)
			go func() {
				_, err := SubAgentToolDef(deps, nil).Handler(context.Background(), input)
				done <- err
			}()
			select {
			case <-accepted:
			case <-time.After(time.Second):
				t.Fatal("accepted event not emitted")
			}
			traceRoot := filepath.Join(root, ".steiner", "traces")
			if _, err := os.Stat(traceRoot); !os.IsNotExist(err) {
				t.Fatalf("trace root exists before accepted event is released: err=%v", err)
			}
			close(release)
			deadline := time.After(time.Second)
			for {
				if _, err := os.Stat(traceRoot); err == nil {
					break
				}
				select {
				case <-deadline:
					t.Fatal("accepted preparation did not create trace root")
				default:
					time.Sleep(time.Millisecond)
				}
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("handler returned error: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("handler did not return")
			}
		})
	}
}

func TestRejectedGroupAdmissionDoesNotPrepareChild(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	deps := minimalDeps(nil)
	deps.WorkDir = root
	deps.AsyncSubAgents = true
	deps.Supervisor = NewSupervisor(SupervisorOptions{MaxParallel: 1})
	input := subAgentTask(AgentTypeExplore, "inspect")
	input["group"] = "named"
	if _, err := SubAgentToolDef(deps, nil).Handler(context.Background(), input); err == nil {
		t.Fatal("group without a tool batch was accepted")
	}
	if _, err := os.Stat(filepath.Join(root, ".steiner", "traces")); !os.IsNotExist(err) {
		t.Fatalf("rejected admission created trace root: err=%v", err)
	}
}

func acceptedBarrierSink(root string, accepted, release chan struct{}, t *testing.T) output.EventSink {
	return output.SinkFunc(func(event output.Event) {
		if event.Type != output.EventTypeDelegationAccepted {
			return
		}
		if _, err := os.Stat(filepath.Join(root, ".steiner", "traces")); !os.IsNotExist(err) {
			t.Errorf("trace root exists during accepted event: err=%v", err)
		}
		close(accepted)
		<-release
	})
}
