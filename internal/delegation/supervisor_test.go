package delegation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/tool"
)

func TestSupervisorBasicSpawning(t *testing.T) {
	t.Parallel()

	controller := NewActiveController()
	s := NewSupervisor(SupervisorOptions{
		MaxParallel: 2,
		Controller:  controller,
	})

	done := make(chan struct{})
	childJob := ChildJob{
		AgentID:      "test",
		AgentType:    AgentTypeCode,
		ParentCallID: "parent",
		Worktree:     CodeWorktree{Path: "/tmp/test"},
		Execute: func(ctx context.Context) (tool.ExecutionResult, error) {
			<-done
			return tool.ExecutionResult{Value: "success"}, nil
		},
		OnCancelledBeforeStart: func() tool.ExecutionResult {
			return tool.ExecutionResult{Value: "cancelled"}
		},
	}

	resultCh := make(chan tool.ExecutionResult)
	go func() {
		res, _ := s.SpawnAndWait(context.Background(), childJob)
		resultCh <- res
	}()

	time.Sleep(50 * time.Millisecond)
	close(done)

	result := <-resultCh
	if result.Value != "success" {
		t.Fatalf("result = %v, want success", result.Value)
	}
}

func TestSupervisorOutstandingCapRejection(t *testing.T) {
	t.Parallel()

	controller := NewActiveController()
	s := NewSupervisor(SupervisorOptions{
		MaxParallel: 2,
		Controller:  controller,
	})

	jobs := make([]chan struct{}, 5)
	for i := range jobs {
		jobs[i] = make(chan struct{})
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			childJob := ChildJob{
				AgentID:      "agent" + string(rune('0'+idx)),
				AgentType:    AgentTypeCode,
				ParentCallID: "parent",
				Worktree:     CodeWorktree{},
				Execute: func(ctx context.Context) (tool.ExecutionResult, error) {
					<-jobs[idx]
					return tool.ExecutionResult{Value: "done"}, nil
				},
				OnCancelledBeforeStart: func() tool.ExecutionResult {
					return tool.ExecutionResult{Value: "cancelled"}
				},
			}
			_, _ = s.SpawnAndWait(context.Background(), childJob)
		}(i)
	}

	time.Sleep(100 * time.Millisecond)

	childJob := ChildJob{
		AgentID:      "agent4",
		AgentType:    AgentTypeCode,
		ParentCallID: "parent",
		Worktree:     CodeWorktree{},
		Execute: func(ctx context.Context) (tool.ExecutionResult, error) {
			return tool.ExecutionResult{Value: "done"}, nil
		},
		OnCancelledBeforeStart: func() tool.ExecutionResult {
			return tool.ExecutionResult{Value: "cancelled"}
		},
	}
	_, err := s.SpawnAndWait(context.Background(), childJob)

	if !errors.Is(err, ErrOutstandingCap) {
		t.Fatalf("error = %v, want ErrOutstandingCap", err)
	}

	for i := 0; i < 4; i++ {
		close(jobs[i])
	}

	wg.Wait()
}

func TestSupervisorCancelledQueuedJob(t *testing.T) {
	t.Parallel()

	controller := NewActiveController()
	s := NewSupervisor(SupervisorOptions{
		MaxParallel: 1,
		Controller:  controller,
	})

	job0Done := make(chan struct{})
	result0 := make(chan tool.ExecutionResult)

	go func() {
		childJob := ChildJob{
			AgentID:      "agent0",
			AgentType:    AgentTypeCode,
			ParentCallID: "parent",
			Worktree:     CodeWorktree{},
			Execute: func(ctx context.Context) (tool.ExecutionResult, error) {
				<-job0Done
				return tool.ExecutionResult{Value: "success"}, nil
			},
			OnCancelledBeforeStart: func() tool.ExecutionResult {
				return tool.ExecutionResult{Value: "cancelled"}
			},
		}
		res, _ := s.SpawnAndWait(context.Background(), childJob)
		result0 <- res
	}()

	time.Sleep(50 * time.Millisecond)

	result1 := make(chan tool.ExecutionResult)
	go func() {
		childJob := ChildJob{
			AgentID:      "agent1",
			AgentType:    AgentTypeCode,
			ParentCallID: "parent",
			Worktree:     CodeWorktree{},
			Execute: func(ctx context.Context) (tool.ExecutionResult, error) {
				return tool.ExecutionResult{Value: "should-not-run"}, nil
			},
			OnCancelledBeforeStart: func() tool.ExecutionResult {
				return tool.ExecutionResult{Value: "queued-cancelled"}
			},
		}
		res, _ := s.SpawnAndWait(context.Background(), childJob)
		result1 <- res
	}()

	time.Sleep(50 * time.Millisecond)

	s.CancelAgent("agent1", false, CancelCauseSystem)

	res1 := <-result1
	if res1.Value != "queued-cancelled" {
		t.Fatalf("cancelled queued job result = %q, want queued-cancelled", res1.Value)
	}

	close(job0Done)
	<-result0
}

func TestSupervisorShutdownWithUnjoinedChild(t *testing.T) {
	t.Parallel()

	controller := NewActiveController()
	s := NewSupervisor(SupervisorOptions{
		MaxParallel: 1,
		Controller:  controller,
		JoinTimeout: 50 * time.Millisecond,
	})

	jobDone := make(chan struct{})

	go func() {
		childJob := ChildJob{
			AgentID:      "agent",
			AgentType:    AgentTypeCode,
			ParentCallID: "parent",
			Worktree:     CodeWorktree{Path: "/tmp/wt-test", Branch: "delegate/test"},
			Execute: func(ctx context.Context) (tool.ExecutionResult, error) {
				<-jobDone
				return tool.ExecutionResult{Value: "done"}, nil
			},
			OnCancelledBeforeStart: func() tool.ExecutionResult {
				return tool.ExecutionResult{Value: "cancelled"}
			},
		}
		_, _ = s.SpawnAndWait(context.Background(), childJob)
	}()

	time.Sleep(50 * time.Millisecond)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer shutdownCancel()

	report := s.Shutdown(shutdownCtx, CancelCauseSystem)

	if len(report.Unjoined) != 1 {
		t.Fatalf("unjoined count = %d, want 1", len(report.Unjoined))
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
		t.Fatalf("protected worktrees missing /tmp/wt-test: %v", protected)
	}

	close(jobDone)
}

func TestSupervisorSpawnAfterShutdown(t *testing.T) {
	t.Parallel()

	controller := NewActiveController()
	s := NewSupervisor(SupervisorOptions{
		MaxParallel: 1,
		Controller:  controller,
	})

	childJob := ChildJob{
		AgentID:      "agent",
		AgentType:    AgentTypeCode,
		ParentCallID: "parent",
		Worktree:     CodeWorktree{},
		Execute: func(ctx context.Context) (tool.ExecutionResult, error) {
			return tool.ExecutionResult{Value: "done"}, nil
		},
		OnCancelledBeforeStart: func() tool.ExecutionResult {
			return tool.ExecutionResult{Value: "cancelled"}
		},
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer shutdownCancel()
	s.Shutdown(shutdownCtx, CancelCauseSystem)

	_, err := s.SpawnAndWait(context.Background(), childJob)

	if !errors.Is(err, ErrSupervisorClosed) {
		t.Fatalf("spawn after shutdown error = %v, want ErrSupervisorClosed", err)
	}
}
