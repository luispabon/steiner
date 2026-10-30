package interactive

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

// TestDriverLastMainRequestReachesNextRun runs a real ConversationDriver over
// driverRun for three submissions. Run 1 always returns req1, and run 2 must
// receive it. Run 2's outcome varies, and run 3 must receive what the driver
// carries after it: a non-zero request replaces the carried value, a failed
// run that returns zero keeps it, and a success that returns zero resets it.
func TestDriverLastMainRequestReachesNextRun(t *testing.T) {
	t.Parallel()
	req1 := agent.LastMainRequest{At: time.Unix(100, 0), ProviderAlias: "openai", BackendModelID: "gpt-1", PromptCacheKey: "cache-key"}
	req2 := agent.LastMainRequest{At: time.Unix(200, 0), ProviderAlias: "openai", BackendModelID: "gpt-2", PromptCacheKey: "cache-key"}
	boom := errors.New("boom")
	tests := []struct {
		name string
		// run2Req and run2Err are what run 2 returns.
		run2Req agent.LastMainRequest
		run2Err error
		// want is what run 3 receives.
		want agent.LastMainRequest
	}{
		{name: "success with a request replaces the carried request", run2Req: req2, want: req2},
		{name: "success with zero resets the carried request", want: agent.LastMainRequest{}},
		{name: "error with a request replaces the carried request", run2Req: req2, run2Err: boom, want: req2},
		{name: "error with zero keeps the carried request", run2Err: boom, want: req1},
		{name: "cancel with zero keeps the carried request", run2Err: context.Canceled, want: req1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var inputs []RunInput
			s := testNewSession(t, Dependencies{})
			s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
				inputs = append(inputs, in)
				conv := append(slices.Clone(in.Conversation), agent.Message{Role: agent.MessageRoleAssistant, Content: "ok"})
				switch len(inputs) {
				case 1:
					return RunResult{Conversation: conv, LastMainRequest: req1}, nil
				case 2:
					return RunResult{Conversation: conv, LastMainRequest: tt.run2Req}, tt.run2Err
				default:
					return RunResult{Conversation: conv}, nil
				}
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
			if inputs[0].LastMainRequest != (agent.LastMainRequest{}) {
				t.Errorf("first run LastMainRequest = %+v, want zero", inputs[0].LastMainRequest)
			}
			if inputs[1].LastMainRequest != req1 {
				t.Errorf("second run LastMainRequest = %+v, want run 1's %+v", inputs[1].LastMainRequest, req1)
			}
			if inputs[2].LastMainRequest != tt.want {
				t.Errorf("third run LastMainRequest = %+v, want %+v", inputs[2].LastMainRequest, tt.want)
			}
		})
	}
}
