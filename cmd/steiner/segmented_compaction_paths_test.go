package main

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// segmentChainConversation returns a conversation whose leading summary
// messages form a valid immutable segment chain. A request projected from a
// conversation with a valid chain marks the last segment as the cache boundary
// only when the segmented compaction policy is enabled, so the marker shows
// whether the policy reached the run.
func segmentChainConversation(prompt string) []agent.Message {
	conversation := []agent.Message{
		{Role: agent.MessageRoleSummary, Content: "segment one", Segment: &agent.SummarySegment{Sequence: 1, SourceMessageCount: 2}},
		{Role: agent.MessageRoleSummary, Content: "segment two", Segment: &agent.SummarySegment{Sequence: 2, SourceMessageCount: 2}},
	}
	if prompt != "" {
		conversation = append(conversation, agent.Message{Role: agent.MessageRoleUser, Content: prompt})
	}
	return conversation
}

// segmentBoundaryContent returns the content of the request message marked as
// the immutable segment boundary, or "" when the request carries no marker.
func segmentBoundaryContent(messages []provider.Message) string {
	for _, message := range messages {
		if message.CacheBoundary {
			return message.Content
		}
	}
	return ""
}

// requireSegmentBoundary asserts the request marker matches wantBoundary. The
// marked message must be the chain's last segment, not an arbitrary message.
func requireSegmentBoundary(t *testing.T, messages []provider.Message, wantBoundary bool) {
	t.Helper()
	got := segmentBoundaryContent(messages)
	if !wantBoundary {
		if got != "" {
			t.Fatalf("request marked %q as the segment boundary, want no marker with the policy disabled", got)
		}
		return
	}
	if got == "" {
		t.Fatal("request carries no segment boundary marker, want the last immutable segment marked")
	}
	if !strings.Contains(got, "segment two") {
		t.Fatalf("segment boundary message = %q, want the chain's last segment", got)
	}
}

// segmentedPolicyCase pairs a policy with the subtest name that reports it.
type segmentedPolicyCase struct {
	name   string
	policy config.SegmentedCompactionPolicy
}

// segmentedPolicyCases is the enabled/disabled pair every marker test runs, so
// each test proves the marker follows the policy rather than the fixture.
func segmentedPolicyCases() []segmentedPolicyCase {
	return []segmentedPolicyCase{
		{name: "policy enabled", policy: nonDefaultSegmentedPolicy()},
		{name: "policy disabled", policy: config.SegmentedCompactionPolicy{}},
	}
}

// segmentedRuntimeFixture returns a runtime carrying policy and the fake
// provider whose captured requests show whether the policy reached the run.
func segmentedRuntimeFixture(t *testing.T, policy config.SegmentedCompactionPolicy) (cliRuntime, *fakeProvider) {
	t.Helper()
	prov := &fakeProvider{responses: []provider.ChatResponse{{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "answer"},
		FinishReason: "stop",
	}}}
	rt := cliRuntime{
		cfg:                 testRuntimeConfig("test-model"),
		provider:            prov,
		registry:            tool.NewRegistry(),
		workDir:             t.TempDir(),
		homeDir:             t.TempDir(),
		events:              output.NoopSink{},
		segmentedCompaction: policy,
	}
	return rt, prov
}

// TestInteractiveRunMarksSegmentBoundaryFromPolicy proves the interactive
// runner that wireInteractiveRunner installs on the session carries the
// runtime's segmented compaction policy into the request the session sends.
func TestInteractiveRunMarksSegmentBoundaryFromPolicy(t *testing.T) {
	for _, tt := range segmentedPolicyCases() {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			rt, prov := segmentedRuntimeFixture(t, tt.policy)
			sess, err := interactive.NewSession(interactive.Dependencies{
				BaseEvents: output.NoopSink{},
				Config:     rt.cfg,
				WorkDir:    rt.workDir,
				HomeDir:    rt.homeDir,
			})
			if err != nil {
				t.Fatalf("NewSession() error = %v", err)
			}
			t.Cleanup(func() { sess.Close(context.Background()) })
			sess.SetConversation(segmentChainConversation(""))
			wireInteractiveRunner(rt, sess)

			if err := sess.Handle(ctx, interactive.SubmitPrompt{Text: "hello"}); err != nil {
				t.Fatalf("Handle(SubmitPrompt) error = %v", err)
			}
			if !sess.WaitRuns(ctx) {
				t.Fatal("WaitRuns() did not settle")
			}
			if len(prov.requests) == 0 {
				t.Fatal("no provider request captured")
			}
			requireSegmentBoundary(t, prov.requests[0].Messages, tt.policy.Enabled)
		})
	}
}

// TestPhaseRunMarksSegmentBoundaryFromPolicy proves phaseRunner.RunPhase drives
// its runner with the runtime it was built from, so the phase run's request
// carries that runtime's segmented compaction policy.
func TestPhaseRunMarksSegmentBoundaryFromPolicy(t *testing.T) {
	for _, tt := range segmentedPolicyCases() {
		t.Run(tt.name, func(t *testing.T) {
			oldBuildPhaseRuntime := buildPhaseRuntime
			t.Cleanup(func() { buildPhaseRuntime = oldBuildPhaseRuntime })

			rt, prov := segmentedRuntimeFixture(t, tt.policy)
			buildPhaseRuntime = func(context.Context, *cobra.Command, *cliFlags, string, string, string) (cliRuntime, error) {
				return rt, nil
			}
			phase, err := newPhaseRunner(context.Background(), &cobra.Command{}, &cliFlags{}, phaseRunnerParams{MaxTurns: 1})
			if err != nil {
				t.Fatalf("newPhaseRunner() error = %v", err)
			}

			if _, err := phase.RunPhase(context.Background(), oneshot.PhaseRunInput{
				Conversation: segmentChainConversation("hello"),
				Session: oneshot.PhaseSession{
					ID:   "phase-segmented",
					Save: func(context.Context, agent.DriverSnapshot) error { return nil },
				},
			}); err != nil {
				t.Fatalf("RunPhase() error = %v", err)
			}
			if len(prov.requests) == 0 {
				t.Fatal("no provider request captured")
			}
			requireSegmentBoundary(t, prov.requests[0].Messages, tt.policy.Enabled)
		})
	}
}

// TestExecRunnerCarriesSegmentedCompaction proves the runner exec wiring builds
// keeps the runtime's segmented compaction policy, which buildRunRequest copies
// into the request the run sends.
func TestExecRunnerCarriesSegmentedCompaction(t *testing.T) {
	for _, tt := range segmentedPolicyCases() {
		t.Run(tt.name, func(t *testing.T) {
			rt := cliRuntime{segmentedCompaction: tt.policy, events: output.NoopSink{}}
			runner := execRunner(rt, "cache-key", prompt.SessionDate{}, 2, false)

			req := buildRunRequest(runner, runnerSetup{}, tool.NewRegistry(), nil, runHooks{})
			if req.SegmentedCompaction != tt.policy {
				t.Fatalf("exec request SegmentedCompaction = %+v, want %+v", req.SegmentedCompaction, tt.policy)
			}
		})
	}
}
