package delegation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tool"
)

// asyncTestDeps returns async deps whose runner blocks until gate is closed.
func asyncTestDeps(maxParallel int, gate chan struct{}) (SpecializedToolDeps, *Supervisor, *channelSink) {
	deps := minimalDeps(&mockRunner{runFunc: func(ctx context.Context, _ agent.RunRequest) (agent.RunState, error) {
		select {
		case <-gate:
		case <-ctx.Done():
		}
		return successRunState(), nil
	}})
	deps.SubAgentCfg.MaxParallel = maxParallel
	deps.SessionStore = NewSessionStore()
	deps.AsyncSubAgents = true
	sup, sink := newAsyncSupervisor(maxParallel, nil)
	deps.Supervisor = sup
	return deps, sup, sink
}

func spawnSubAgent(ctx context.Context, t *testing.T, deps SpecializedToolDeps, input map[string]any) (AckResult, error) {
	t.Helper()
	got, err := SubAgentToolDef(deps, nil).Handler(ctx, input)
	if err != nil {
		return AckResult{}, err
	}
	res, ok := got.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("handler result = %T, want tool.ExecutionResult", got)
	}
	ack, ok := res.Value.(AckResult)
	if !ok {
		t.Fatalf("result value = %T, want AckResult", res.Value)
	}
	if res.Retention == nil || res.Retention.Kind != tool.RetentionKindDelegateSummary || res.Retention.Status != ack.status() || res.Retention.AgentID != ack.AgentID {
		t.Fatalf("retention = %+v, want delegate summary %q for %q", res.Retention, ack.status(), ack.AgentID)
	}
	return ack, nil
}

func recvBatch(t *testing.T, sink *channelSink) []agent.SubAgentCompletion {
	t.Helper()
	select {
	case batch := <-sink.ch:
		return batch
	case <-time.After(5 * time.Second):
		t.Fatal("no completion delivered")
		return nil
	}
}

func TestAsyncSubAgentReturnsAckThenDeliversCompletion(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	deps, sup, sink := asyncTestDeps(2, gate)

	ack, err := spawnSubAgent(context.Background(), t, deps, subAgentTask(AgentTypeExplore, "look"))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if ack.AgentID == "" || ack.Queued {
		t.Fatalf("ack = %+v, want running agent", ack)
	}
	env := ack.ProjectToolResult()
	if env.Status != "running" || env.Continuation == nil || env.Continuation.AgentID != ack.AgentID || env.Output == "" {
		t.Fatalf("projection = %+v, want running ack with continuation and output", env)
	}
	sink.none(t)
	if !sup.IsPending(ack.AgentID) {
		t.Fatal("agent not pending after ack")
	}

	close(gate)
	batch := recvBatch(t, sink)
	if len(batch) != 1 || batch[0].AgentID != ack.AgentID || batch[0].Status != string(StatusComplete) {
		t.Fatalf("batch = %+v, want one complete completion for %s", batch, ack.AgentID)
	}
}

func TestAsyncSubAgentQueuedWhenOverMaxParallel(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	deps, _, _ := asyncTestDeps(1, gate)
	defer close(gate)

	first, err := spawnSubAgent(context.Background(), t, deps, subAgentTask(AgentTypeExplore, "one"))
	if err != nil || first.Queued {
		t.Fatalf("first = %+v, err = %v, want running", first, err)
	}
	second, err := spawnSubAgent(context.Background(), t, deps, subAgentTask(AgentTypeExplore, "two"))
	if err != nil || !second.Queued {
		t.Fatalf("second = %+v, err = %v, want queued", second, err)
	}
	if got := second.ProjectToolResult().Status; got != "queued" {
		t.Fatalf("queued projection status = %q, want queued", got)
	}
}

func TestAsyncSubAgentOutstandingCapRejected(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	deps, _, _ := asyncTestDeps(1, gate)
	defer close(gate)

	for i := range 2 {
		if _, err := spawnSubAgent(context.Background(), t, deps, subAgentTask(AgentTypeExplore, "task")); err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
	}
	_, err := spawnSubAgent(context.Background(), t, deps, subAgentTask(AgentTypeExplore, "over cap"))
	if !errors.Is(err, ErrOutstandingCap) {
		t.Fatalf("err = %v, want ErrOutstandingCap", err)
	}
}

func TestAsyncSubAgentGroupDeliveredTogetherAfterSeal(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	deps, sup, sink := asyncTestDeps(2, gate)
	ctx := agent.WithToolBatchID(context.Background(), "batch-1")

	var ids []string
	for _, desc := range []string{"a", "b"} {
		input := subAgentTask(AgentTypeExplore, desc)
		input["group"] = "pair"
		ack, err := spawnSubAgent(ctx, t, deps, input)
		if err != nil {
			t.Fatalf("spawn %s: %v", desc, err)
		}
		ids = append(ids, ack.AgentID)
	}
	close(gate)
	sink.none(t)
	waitFor(t, func() bool {
		for _, entry := range sup.Pending() {
			if entry.State != agent.SubAgentFinished {
				return false
			}
		}
		return true
	})
	sink.none(t)

	sup.SealBatch("batch-1")
	batch := recvBatch(t, sink)
	if len(batch) != 2 || batch[0].Seq >= batch[1].Seq {
		t.Fatalf("batch = %+v, want two completions in ascending Seq order", batch)
	}
	got := map[string]bool{batch[0].AgentID: true, batch[1].AgentID: true}
	if !got[ids[0]] || !got[ids[1]] {
		t.Fatalf("batch = %+v, want both agents %v together", batch, ids)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAsyncFollowUpRejectedWhilePendingThenAcks(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	deps, sup, sink := asyncTestDeps(2, gate)

	ack, err := spawnSubAgent(context.Background(), t, deps, subAgentTask(AgentTypeExplore, "look"))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	followUp := NewFollowUpHandler(deps.SubAgentHandlerDeps)
	input := map[string]any{"agent_id": ack.AgentID, "message": "more"}

	if _, err := followUp(context.Background(), input); err == nil {
		t.Fatal("follow_up on running agent: want error")
	}
	close(gate)
	batch := recvBatch(t, sink)
	if _, err := followUp(context.Background(), input); err == nil {
		t.Fatal("follow_up on finished-but-undelivered agent: want error")
	}

	sup.MarkDelivered([]string{batch[0].ParentCallID})
	got, err := followUp(context.Background(), input)
	if err != nil {
		t.Fatalf("follow_up after delivery: %v", err)
	}
	res, ok := got.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("follow_up result = %T, want tool.ExecutionResult", got)
	}
	if fa, ok := res.Value.(AckResult); !ok || fa.AgentID != ack.AgentID {
		t.Fatalf("follow_up value = %#v, want ack for %s", res.Value, ack.AgentID)
	}
}

func TestAsyncVisionSubAgentReturnsAck(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	deps, _, sink := asyncTestDeps(2, gate)
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "test.png")
	if err := os.WriteFile(imgPath, []byte("fake-png-content"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	store := agent.NewImageStore(dir)
	ref := store.Register(imgPath, "image/png", 10, 20, 15)
	deps.ImageStore = store

	ack, err := spawnSubAgent(context.Background(), t, deps, subAgentTaskWithImageID("describe", ref.ID))
	if err != nil {
		t.Fatalf("vision spawn: %v", err)
	}
	close(gate)
	if batch := recvBatch(t, sink); len(batch) != 1 || batch[0].AgentID != ack.AgentID {
		t.Fatalf("batch = %+v, want completion for %s", batch, ack.AgentID)
	}
}

func TestBlockingSubAgentReturnsResultAndPostsNothing(t *testing.T) {
	t.Parallel()
	deps := minimalDeps(&mockRunner{runFunc: func(context.Context, agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})
	sup, sink := newAsyncSupervisor(2, nil)
	deps.Supervisor = sup

	got, err := SubAgentToolDef(deps, nil).Handler(context.Background(), subAgentTask(AgentTypeExplore, "look"))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	res, ok := got.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("handler result = %T, want tool.ExecutionResult", got)
	}
	if _, ok := res.Value.(Result); !ok {
		t.Fatalf("value = %T, want blocking Result", res.Value)
	}
	sink.none(t)
}

func TestSubAgentSchemaGroupOnlyWhenAsync(t *testing.T) {
	t.Parallel()
	for _, async := range []bool{false, true} {
		deps := minimalDeps(&mockRunner{})
		deps.AsyncSubAgents = async
		props, _ := SubAgentToolDef(deps, nil).ParameterSchema["properties"].(map[string]any)
		if _, has := props["group"]; has != async {
			t.Errorf("async=%v: group present = %v", async, has)
		}
	}
}
