package delegation

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// orderedProvider records a child run's release alongside the runner events, so
// tests can prove the connection is released after the run, not before it.
type orderedProvider struct {
	mu     sync.Mutex
	events []string
}

func (p *orderedProvider) record(event string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, event)
}

func (p *orderedProvider) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.events...)
}

func (p *orderedProvider) releaseCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for _, event := range p.events {
		if event == "release" {
			count++
		}
	}
	return count
}

func (p *orderedProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, errors.New("not implemented")
}

func (p *orderedProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, errors.New("not implemented")
}

func (p *orderedProvider) SupportsUsageStats() bool { return false }

func (p *orderedProvider) ReleaseConnection() { p.record("release") }

// eventRunner records its run once around a child run, optionally blocking
// between the two events.
type eventRunner struct {
	prov    *orderedProvider
	started chan<- struct{}
	unblock <-chan struct{}
}

func (r *eventRunner) Run(context.Context, agent.RunRequest) (agent.RunState, error) {
	r.prov.record("run-start")
	if r.started != nil {
		r.started <- struct{}{}
	}
	if r.unblock != nil {
		<-r.unblock
	}
	r.prov.record("run-end")
	return agent.RunState{
		Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "done"}},
		TurnCount:    1,
		TokenCount:   10,
		StopReason:   agent.StopReasonComplete,
	}, nil
}

type releaseTestSetup struct {
	spec Spec
	req  agent.RunRequest
}

func newReleaseTestSetup(t *testing.T, prov provider.Provider) releaseTestSetup {
	t.Helper()
	spec := makeSpec("release-agent", 1000)
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	limits := agent.Limits{MaxTurns: 5}
	return releaseTestSetup{
		spec: spec,
		req:  testChildRunRequest(spec, prov, visibleReg, execReg, limits, output.NoopSink{}),
	}
}

func TestSpawnDelegateReleasesChildProviderAfterRun(t *testing.T) {
	prov := &orderedProvider{}
	setup := newReleaseTestSetup(t, prov)
	runner := &eventRunner{prov: prov}

	if _, _, _, err := SpawnDelegate(context.Background(), setup.spec, setup.req, runner, output.NoopSink{}, nil); err != nil {
		t.Fatalf("SpawnDelegate: %v", err)
	}

	want := []string{"run-start", "run-end", "release"}
	got := prov.snapshot()
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
}

// TestSpawnDelegateReleasesAfterAsyncCompletion proves an async child's release
// lands after the supervisor worker's run finishes, not when the child is
// dispatched.
func TestSpawnDelegateReleasesAfterAsyncCompletion(t *testing.T) {
	prov := &orderedProvider{}
	setup := newReleaseTestSetup(t, prov)
	started := make(chan struct{})
	unblock := make(chan struct{})
	runner := &eventRunner{prov: prov, started: started, unblock: unblock}

	done := make(chan error, 1)
	go func() {
		_, _, _, err := SpawnDelegate(context.Background(), setup.spec, setup.req, runner, output.NoopSink{}, nil)
		done <- err
	}()

	<-started
	if got := prov.releaseCount(); got != 0 {
		t.Fatalf("releases while the child runs = %d, want 0", got)
	}

	close(unblock)
	if err := <-done; err != nil {
		t.Fatalf("SpawnDelegate: %v", err)
	}

	want := []string{"run-start", "run-end", "release"}
	got := prov.snapshot()
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
}
