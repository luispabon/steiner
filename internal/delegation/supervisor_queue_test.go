package delegation

import (
	"context"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/tool"
)

func TestSupervisorQueueCapBehavior(t *testing.T) {
	t.Parallel()

	controller := NewActiveController()
	s := NewSupervisor(SupervisorOptions{
		MaxParallel: 2,
		Controller:  controller,
	})

	makeBlocking := func(agentID string) ChildJob {
		done := make(chan struct{})
		return ChildJob{
			AgentID:      agentID,
			AgentType:    AgentTypeCode,
			ParentCallID: "parent",
			Worktree:     CodeWorktree{Path: "/tmp/" + agentID},
			Execute: func(ctx context.Context) (tool.ExecutionResult, error) {
				<-done
				return tool.ExecutionResult{Value: agentID + "-done"}, nil
			},
			OnCancelledBeforeStart: func() tool.ExecutionResult {
				return tool.ExecutionResult{Value: agentID + "-cancelled"}
			},
		}
	}

	results := make(chan error, 5)

	for i := 0; i < 4; i++ {
		go func(id string) {
			_, err := s.SpawnAndWait(context.Background(), makeBlocking(id))
			results <- err
		}("job" + string(rune('0'+i)))
	}

	time.Sleep(200 * time.Millisecond)

	job4 := makeBlocking("job4")
	_, err := s.SpawnAndWait(context.Background(), job4)
	if err == nil {
		t.Fatal("5th job should error with outstanding cap")
	}

	for i := 0; i < 4; i++ {
		<-results
	}
}
