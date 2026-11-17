package mcp

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// planModeFixture connects one allow-mode fixture server, whose tool
// definitions resolve plan mode through the manager at call time.
func planModeFixture(t *testing.T) *Manager {
	t.Helper()
	cfg := config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"fixture": {Enabled: true, Command: buildFixture(t), Approval: "allow"},
		},
	}
	m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, ConnectOptions{WarnFn: func(string) {}, InfoFn: func(string) {}, Stderr: io.Discard})
	t.Cleanup(func() { _ = m.Close() })
	waitInit(t, m)
	return m
}

// countingApprover counts approval requests and always allows them.
type countingApprover struct{ count atomic.Int64 }

func (c *countingApprover) RequestApproval(_ context.Context, req tool.ApprovalRequest) error {
	c.count.Add(1)
	req.Response <- tool.ApprovalResponse{Allow: true}
	return nil
}

// TestUpdatePlanModeAffectsRegisteredTools proves a mode switch reaches tool
// definitions created at connect time: an allow-mode server prompts only while
// plan mode is on, with no registry rebuild. It also pins the nil-receiver
// contract, which must not panic.
func TestUpdatePlanModeAffectsRegisteredTools(t *testing.T) {
	var nilManager *Manager
	nilManager.UpdatePlanMode(true)

	m := planModeFixture(t)
	approver := &countingApprover{}
	m.UpdateApprover(approver)
	def := findTool(t, m.ToolDefs(), "mcp__fixture__echo")

	call := func(wantPrompts int64) {
		t.Helper()
		env, err := def.Handler(context.Background(), map[string]any{"text": "hi"})
		if err != nil {
			t.Fatalf("handler returned Go error %v, want nil", err)
		}
		if text, ok := env.(string); !ok || text != "hi" {
			t.Fatalf("handler result = %#v, want %q", env, "hi")
		}
		if got := approver.count.Load(); got != wantPrompts {
			t.Fatalf("approval requests = %d, want %d", got, wantPrompts)
		}
	}

	call(0)
	m.UpdatePlanMode(true)
	call(1)
	m.UpdatePlanMode(false)
	call(1)
}

// TestUpdatePlanModeConcurrentToolCalls runs mode switches concurrently with
// tool calls, which resolve the mode through the manager mutex. The race
// detector is the assertion: an unsynchronised flag read or write fails here.
func TestUpdatePlanModeConcurrentToolCalls(t *testing.T) {
	m := planModeFixture(t)
	m.UpdateApprover(allowApprover())
	def := findTool(t, m.ToolDefs(), "mcp__fixture__echo")

	const iterations = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range iterations {
		wg.Add(1)
		go func(planMode bool) {
			defer wg.Done()
			<-start
			m.UpdatePlanMode(planMode)
		}(i%2 == 0)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := def.Handler(context.Background(), map[string]any{"text": "hi"}); err != nil {
				t.Errorf("handler returned Go error %v, want nil", err)
			}
		}()
	}
	close(start)
	wg.Wait()
}
