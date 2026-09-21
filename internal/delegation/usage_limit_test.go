package delegation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
)

func testUsageLimitErr() error {
	return fmt.Errorf("run: %w", &provider.UsageLimitError{
		Kind:     provider.UsageLimitKindUsage,
		Provider: "codex",
		Model:    "codex/luna/high",
		Message:  "limit hit",
		ResetsAt: time.Date(2026, 9, 21, 15, 34, 0, 0, time.UTC),
	})
}

func TestFailedDelegateReason_UsageLimitNotice(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		err        error
		wantNotice bool
	}{
		{name: "usage limit", err: testUsageLimitErr(), wantNotice: true},
		{name: "plain error", err: errors.New("boom"), wantNotice: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reason := failedDelegateReason(tt.err, agent.RunState{})
			if !strings.HasPrefix(reason, "delegation failed: ") {
				t.Errorf("reason = %q, want delegation failed prefix", reason)
			}
			if got := strings.Contains(reason, usageLimitDelegateNotice); got != tt.wantNotice {
				t.Errorf("notice present = %v, want %v; reason = %q", got, tt.wantNotice, reason)
			}
			if tt.wantNotice && !strings.Contains(reason, "provider usage limit reached (codex/luna/high") {
				t.Errorf("reason missing readable usage-limit head: %q", reason)
			}
		})
	}
}

func TestFailedDelegateExecution_UsageLimit(t *testing.T) {
	t.Parallel()
	res := failedDelegateExecution(Spec{AgentID: "a"}, agent.RunState{}, TokenUsage{}, testUsageLimitErr(), newTraceCollector("a", "task"), nil)
	result := res.Value.(Result)
	if result.Status != StatusFailed {
		t.Errorf("status = %q, want %q", result.Status, StatusFailed)
	}
	if result.SessionResumable {
		t.Error("SessionResumable = true, want false")
	}
	if !strings.Contains(result.Reason, usageLimitDelegateNotice) {
		t.Errorf("reason missing notice: %q", result.Reason)
	}
}

func TestBuildResult_UsageLimitStopReason(t *testing.T) {
	t.Parallel()
	result := BuildResult("a", makeRunState(1, 10, agent.StopReasonUsageLimit, "partial"))
	if result.Status != StatusFailed {
		t.Errorf("status = %q, want %q", result.Status, StatusFailed)
	}
	if strings.Contains(result.Reason, "unknown stop reason") {
		t.Errorf("reason = %q, must not be unknown stop reason", result.Reason)
	}
}

func TestSpawnDelegate_UsageLimitDoesNotPropagate(t *testing.T) {
	t.Parallel()
	runner := &mockRunner{runFunc: func(context.Context, agent.RunRequest) (agent.RunState, error) {
		return agent.RunState{}, testUsageLimitErr()
	}}
	res, _, _, err := SpawnDelegate(context.Background(), Spec{AgentID: "a"}, agent.RunRequest{}, runner, nil, nil)
	if err != nil {
		t.Fatalf("SpawnDelegate error = %v, want nil (no propagation)", err)
	}
	result := res.Value.(Result)
	if result.Status != StatusFailed || !strings.Contains(result.Reason, usageLimitDelegateNotice) {
		t.Errorf("result = %#v, want failed with notice", result)
	}
}
