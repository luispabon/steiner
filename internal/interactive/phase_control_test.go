package interactive

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

type recordingPhaseControl struct {
	mu    sync.Mutex
	calls []string
}

func (c *recordingPhaseControl) record(call string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, call)
}

func (c *recordingPhaseControl) got() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.calls)
}

func (c *recordingPhaseControl) Submit(text string, _ []agent.ImageBlock) { c.record("submit:" + text) }
func (c *recordingPhaseControl) NotifySteer()                             { c.record("steer") }
func (c *recordingPhaseControl) StopTurn()                                { c.record("stop") }
func (c *recordingPhaseControl) CancelAgent(id string, discard bool) error {
	c.record("cancel:" + id)
	if discard {
		return errors.New("discard refused")
	}
	return nil
}
func (c *recordingPhaseControl) CancelAll() error { c.record("cancel-all"); return nil }

func countingRunner(runs *atomic.Int32) *inputRunner {
	return &inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
		runs.Add(1)
		return withAnswer(in, "ok"), nil
	}}
}

func waitForSteersDrained(t *testing.T, steers *agent.SteerQueue) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for steers.Len() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("steer queue len = %d, want the re-attached session driver to drain it", steers.Len())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func handleAll(t *testing.T, s *Session) {
	t.Helper()
	ctx := context.Background()
	for _, action := range []Action{
		SubmitPrompt{Text: "hi"},
		NotifySteer{},
		InterruptActiveRun{},
		CancelDelegate{AgentID: "a1"},
		CancelAllDelegates{},
	} {
		if err := s.Handle(ctx, action); err != nil {
			t.Fatalf("Handle(%T): %v", action, err)
		}
	}
}

func TestPhaseControlReceivesUserControlAndSessionDriverNothing(t *testing.T) {
	t.Parallel()
	canceller := &recordingDelegateCanceller{}
	s := testNewSession(t, Dependencies{DelegateCanceller: canceller})
	var runs atomic.Int32
	s.SetRunner(countingRunner(&runs))
	steers := s.ActiveRunController().SteerQueue()
	pc := &recordingPhaseControl{}

	release := s.SetActivePhaseControl(pc)
	defer release()
	steers.Add(agent.SteerMessage{Text: "steer"})
	handleAll(t, s)
	waitSettled(t, s)

	want := []string{"submit:hi", "steer", "stop", "cancel:a1", "cancel-all"}
	if got := pc.got(); !slices.Equal(got, want) {
		t.Fatalf("phase control calls = %v, want %v", got, want)
	}
	if runs.Load() != 0 {
		t.Fatalf("session driver ran %d times, want 0", runs.Load())
	}
	if got := steers.Len(); got != 1 {
		t.Fatalf("steer queue len = %d, want the steer left for the phase", got)
	}
	if canceller.calls != 0 || canceller.allCalls != 0 {
		t.Fatalf("session canceller called (%d, %d), want untouched", canceller.calls, canceller.allCalls)
	}
}

func TestPhaseControlErrorsReachTheCaller(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{})
	release := s.SetActivePhaseControl(&recordingPhaseControl{})
	defer release()
	if err := s.Handle(context.Background(), CancelDelegate{AgentID: "a1", Discard: true}); err == nil {
		t.Fatal("CancelDelegate error = nil, want the phase control's error")
	}
}

func TestReleasePhaseControlRestoresRouting(t *testing.T) {
	t.Parallel()
	canceller := &recordingDelegateCanceller{}
	s := testNewSession(t, Dependencies{DelegateCanceller: canceller})
	var runs atomic.Int32
	s.SetRunner(countingRunner(&runs))
	steers := s.ActiveRunController().SteerQueue()
	pc := &recordingPhaseControl{}

	release := s.SetActivePhaseControl(pc)
	release()
	release()
	handleAll(t, s)
	waitSettled(t, s)

	if got := pc.got(); len(got) != 0 {
		t.Fatalf("released phase control got %v", got)
	}
	if runs.Load() == 0 {
		t.Fatal("session driver did not run after release")
	}
	if canceller.calls != 1 || canceller.allCalls != 1 {
		t.Fatalf("session canceller calls = (%d, %d), want cancel-one and cancel-all", canceller.calls, canceller.allCalls)
	}

	if err := s.Handle(context.Background(), SubmitPrompt{Text: "lift the hold"}); err != nil {
		t.Fatalf("SubmitPrompt: %v", err)
	}
	waitSettled(t, s)
	steers.Add(agent.SteerMessage{Text: "after release"})
	if err := s.Handle(context.Background(), NotifySteer{}); err != nil {
		t.Fatalf("NotifySteer: %v", err)
	}
	waitForSteersDrained(t, steers)
}

func TestPhaseControlHandlesSwapBetweenPhases(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{})
	var runs atomic.Int32
	s.SetRunner(countingRunner(&runs))
	first, second := &recordingPhaseControl{}, &recordingPhaseControl{}

	releaseFirst := s.SetActivePhaseControl(first)
	releaseFirst()
	releaseSecond := s.SetActivePhaseControl(second)
	if err := s.Handle(context.Background(), InterruptActiveRun{}); err != nil {
		t.Fatalf("InterruptActiveRun: %v", err)
	}
	if got := first.got(); len(got) != 0 {
		t.Fatalf("first phase control got %v after release", got)
	}
	if got := second.got(); !slices.Equal(got, []string{"stop"}) {
		t.Fatalf("second phase control got %v, want [stop]", got)
	}

	replacement := &recordingPhaseControl{}
	releaseReplacement := s.SetActivePhaseControl(replacement)
	releaseSecond()
	if err := s.Handle(context.Background(), InterruptActiveRun{}); err != nil {
		t.Fatalf("InterruptActiveRun: %v", err)
	}
	if got := replacement.got(); !slices.Equal(got, []string{"stop"}) {
		t.Fatalf("replacement got %v: a superseded release must not clear its successor", got)
	}
	releaseReplacement()
	if runs.Load() != 0 {
		t.Fatalf("session driver ran %d times, want 0", runs.Load())
	}
}

func TestDriverBuiltDuringPhaseControlDoesNotDrainSteers(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{Config: guardTestConfig(), SessionStore: newMockSessionStore()})
	var runs atomic.Int32
	s.SetRunner(countingRunner(&runs))
	steers := s.ActiveRunController().SteerQueue()

	release := s.SetActivePhaseControl(&recordingPhaseControl{})
	steers.Add(agent.SteerMessage{Text: "for the phase"})
	if err := s.Handle(context.Background(), ClearConversation{}); err != nil {
		t.Fatalf("ClearConversation: %v", err)
	}
	if err := s.Handle(context.Background(), NotifySteer{}); err != nil {
		t.Fatalf("NotifySteer: %v", err)
	}
	waitSettled(t, s)
	if got := steers.Len(); got != 1 || runs.Load() != 0 {
		t.Fatalf("steers = %d, runs = %d; the successor driver took the phase's steer", got, runs.Load())
	}

	release()
	if err := s.Handle(context.Background(), NotifySteer{}); err != nil {
		t.Fatalf("NotifySteer: %v", err)
	}
	waitForSteersDrained(t, steers)
}

func TestPhaseRouterFollowsRegistrations(t *testing.T) {
	t.Parallel()
	router := &PhaseRouter{}
	if err := router.CancelAll(); err == nil {
		t.Fatal("CancelAll with no phase = nil, want an error")
	}
	if err := router.CancelAgent("a", false); err == nil {
		t.Fatal("CancelAgent with no phase = nil, want an error")
	}
	router.Submit("dropped", nil)
	router.NotifySteer()
	router.StopTurn()

	first, second := &recordingPhaseControl{}, &recordingPhaseControl{}
	releaseFirst := router.Register(first)
	router.Submit("one", nil)
	router.NotifySteer()
	releaseSecond := router.Register(second)
	releaseFirst()
	router.StopTurn()
	if err := router.CancelAll(); err != nil {
		t.Fatalf("CancelAll: %v", err)
	}
	if err := router.CancelAgent("a", false); err != nil {
		t.Fatalf("CancelAgent: %v", err)
	}
	if got, want := first.got(), []string{"submit:one", "steer"}; !slices.Equal(got, want) {
		t.Fatalf("first = %v, want %v", got, want)
	}
	if got, want := second.got(), []string{"stop", "cancel-all", "cancel:a"}; !slices.Equal(got, want) {
		t.Fatalf("second = %v, want %v", got, want)
	}
	releaseSecond()
	releaseSecond()
	router.StopTurn()
	if got := second.got(); len(got) != 3 {
		t.Fatalf("second got %v after release", got)
	}
}

func TestRunBackgroundIsJoinedAndCancelled(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{})
	started := make(chan struct{})
	finished := make(chan struct{})
	s.RunBackground(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(finished)
	})
	recv(t, started, "background start")

	waitCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if s.WaitRuns(waitCtx) {
		t.Fatal("WaitRuns returned while the background run was live")
	}
	s.CancelBackground()
	if !s.WaitRuns(context.Background()) {
		t.Fatal("WaitRuns = false after CancelBackground")
	}
	recv(t, finished, "background exit")
}
