package delegation

import (
	"context"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/tool"
)

func TestSupervisorShutdownBlocksNewSpawns(t *testing.T) {
	t.Parallel()

	controller := NewActiveController()
	s := NewSupervisor(SupervisorOptions{
		MaxParallel: 1,
		Controller:  controller,
	})

	blocked := make(chan struct{})
	done := make(chan struct{})

	go func() {
		childJob := ChildJob{
			AgentID:      "agent",
			AgentType:    AgentTypeCode,
			ParentCallID: "parent",
			Worktree:     CodeWorktree{Path: "/tmp/test"},
			Execute: func(ctx context.Context) (tool.ExecutionResult, error) {
				close(blocked)
				<-done
				return tool.ExecutionResult{Value: "done"}, nil
			},
			OnCancelledBeforeStart: func() tool.ExecutionResult {
				return tool.ExecutionResult{Value: "cancelled"}
			},
		}
		_, _ = s.SpawnAndWait(context.Background(), childJob)
	}()

	<-blocked

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer shutdownCancel()

	s.Shutdown(shutdownCtx, CancelCauseSystem)

	job2 := ChildJob{
		AgentID:      "agent2",
		AgentType:    AgentTypeCode,
		ParentCallID: "parent2",
		Worktree:     CodeWorktree{Path: "/tmp/test2"},
		Execute: func(ctx context.Context) (tool.ExecutionResult, error) {
			return tool.ExecutionResult{Value: "done"}, nil
		},
		OnCancelledBeforeStart: func() tool.ExecutionResult {
			return tool.ExecutionResult{Value: "cancelled"}
		},
	}
	_, err := s.SpawnAndWait(context.Background(), job2)

	if err != ErrSupervisorClosed {
		t.Fatalf("spawn after shutdown returned %v, want ErrSupervisorClosed", err)
	}

	close(done)
}

func TestSupervisorShutdownReportsUnjoinedWorktrees(t *testing.T) {
	t.Parallel()

	controller := NewActiveController()
	s := NewSupervisor(SupervisorOptions{
		MaxParallel: 1,
		Controller:  controller,
		JoinTimeout: 50 * time.Millisecond,
	})

	blocked := make(chan struct{})
	done := make(chan struct{})

	go func() {
		childJob := ChildJob{
			AgentID:      "agent",
			AgentType:    AgentTypeCode,
			ParentCallID: "parent",
			Worktree:     CodeWorktree{Path: "/tmp/wt-test", Branch: "delegate/test"},
			Execute: func(ctx context.Context) (tool.ExecutionResult, error) {
				close(blocked)
				<-done
				return tool.ExecutionResult{Value: "done"}, nil
			},
			OnCancelledBeforeStart: func() tool.ExecutionResult {
				return tool.ExecutionResult{Value: "cancelled"}
			},
		}
		_, _ = s.SpawnAndWait(context.Background(), childJob)
	}()

	<-blocked

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer shutdownCancel()

	report := s.Shutdown(shutdownCtx, CancelCauseSystem)

	if len(report.Unjoined) != 1 {
		t.Fatalf("unjoined count = %d, want 1", len(report.Unjoined))
	}

	if report.Unjoined[0].WorktreePath != "/tmp/wt-test" {
		t.Fatalf("worktree path = %q, want /tmp/wt-test", report.Unjoined[0].WorktreePath)
	}

	protected := s.ProtectedWorktrees()
	found := false
	for _, p := range protected {
		if p == "/tmp/wt-test" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("protected worktrees %v missing /tmp/wt-test", protected)
	}

	close(done)
}
