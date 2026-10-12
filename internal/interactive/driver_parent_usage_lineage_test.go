package interactive

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
)

// TestDriverParentUsageFollowsMergedLineageGeneration runs a real
// ConversationDriver over driverRun and mergeRunLineage. Each run returns the
// same parent usage, so only the merged lineage decides whether the next run is
// seeded: a run that appends a compaction generation must drop it, a run that
// keeps one generation must carry it.
func TestDriverParentUsageFollowsMergedLineageGeneration(t *testing.T) {
	t.Parallel()
	usage := &agent.ParentUsage{ModelBackendID: "model-a", ContextSize: 1000, Usage: &provider.UsageStats{PromptTokens: 800}}
	tests := []struct {
		name     string
		compact  bool
		wantNext *agent.ParentUsage
	}{
		{name: "compaction generation drops usage", compact: true},
		{name: "single generation keeps usage", wantNext: usage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var inputs []RunInput
			s := testNewSession(t, Dependencies{})
			s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
				inputs = append(inputs, in)
				conv := append(slices.Clone(in.Conversation), agent.Message{Role: agent.MessageRoleAssistant, Content: "ok"})
				lineage := lineageFromMessages(conv)
				// Only the second run compacts; its state still reports the usage,
				// so the driver's generation check alone decides what carries.
				if len(inputs) == 2 && tt.compact {
					lineage = lineage.WithNewGeneration(nil, conv)
				}
				return RunResult{Conversation: conv, Lineage: lineage, ParentUsage: usage}, nil
			}})

			conv := []agent.Message{{Role: agent.MessageRoleUser, Content: "earlier"}}
			d := agent.NewConversationDriver(agent.DriverOptions{
				Run: func(ctx context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
					return s.driverRun(ctx, in, "")
				},
			}, conv, lineageFromMessages(conv))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d.Start(ctx)
			for _, text := range []string{"one", "two", "three"} {
				d.Submit(text, nil, agent.SubmitMeta{})
				if err := d.WaitQuiescent(ctx); err != nil {
					t.Fatalf("WaitQuiescent() error = %v", err)
				}
			}
			d.Close(ctx)

			if len(inputs) != 3 {
				t.Fatalf("runs = %d, want 3", len(inputs))
			}
			if inputs[0].ParentUsage != nil {
				t.Errorf("first run ParentUsage = %+v, want nil", inputs[0].ParentUsage)
			}
			if !reflect.DeepEqual(inputs[1].ParentUsage, usage) {
				t.Errorf("second run ParentUsage = %+v, want %+v", inputs[1].ParentUsage, usage)
			}
			if !reflect.DeepEqual(inputs[2].ParentUsage, tt.wantNext) {
				t.Errorf("third run ParentUsage = %+v, want %+v", inputs[2].ParentUsage, tt.wantNext)
			}
		})
	}
}
