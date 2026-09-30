package delegation

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tool"
)

func TestSupervisorShutdownReleasesHeldGroups(t *testing.T) {
	tests := []struct {
		name string
		seal bool
	}{
		{name: "unsealed group", seal: false},
		{name: "sealed group with unjoined member", seal: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, sink := newAsyncSupervisor(2, nil)
			s.joinTimeout = 1
			ctx := agent.WithToolBatchID(context.Background(), "b1")
			done := newAsyncChild("done", "g")
			stuck := make(chan struct{})
			stuckChild := newAsyncChild("stuck", "g")
			stuckChild.job.Execute = func(context.Context) (tool.ExecutionResult, error) {
				close(stuckChild.started)
				<-stuck
				return tool.ExecutionResult{}, nil
			}
			spawnAsync(ctx, t, s, done)
			spawnAsync(ctx, t, s, stuckChild)
			<-stuckChild.started
			close(done.release)
			waitFinished(t, s, "done")
			sink.none(t)
			if tt.seal {
				s.SealBatch("b1")
				sink.none(t)
			}

			report := s.Shutdown(context.Background(), CancelCauseSystem)
			if len(report.Unjoined) != 1 || report.Unjoined[0].AgentID != "stuck" {
				t.Fatalf("report = %+v", report)
			}
			batch := recv(t, sink.ch, "shutdown batch")
			if len(batch) != 2 || batch[0].AgentID != "done" || batch[1].AgentID != "stuck" || batch[0].Seq >= batch[1].Seq {
				t.Fatalf("batch = %+v, want done then stuck ordered by seq", batch)
			}
			if batch[0].Quiet || !batch[1].Quiet || batch[1].Status != "cancelled" {
				t.Fatalf("batch = %+v, want only the unjoined completion quiet and cancelled", batch)
			}
			s.Shutdown(context.Background(), CancelCauseSystem)
			close(stuck)
			sink.none(t)
			s.mu.Lock()
			defer s.mu.Unlock()
			for id, state := range s.jobs {
				if state.held {
					t.Fatalf("job %s still held after shutdown", id)
				}
			}
			if len(s.groups) != 0 {
				t.Fatalf("groups left after shutdown: %d", len(s.groups))
			}
		})
	}
}
