package interactive

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
)

// TestDriverRunCarriesParentUsage proves driverRun hands the driver's parent
// usage to the run and returns the run's usage to the driver, on success and on
// a run error.
func TestDriverRunCarriesParentUsage(t *testing.T) {
	seed := &agent.ParentUsage{ModelBackendID: "model-a", ContextSize: 1000, Usage: &provider.UsageStats{PromptTokens: 800}}
	returned := &agent.ParentUsage{ModelBackendID: "model-a", ContextSize: 1000, Usage: &provider.UsageStats{PromptTokens: 900}}
	tests := []struct {
		name    string
		runErr  error
		wantOut *agent.ParentUsage
	}{
		{name: "success adopts the run's usage", wantOut: returned},
		{name: "error keeps the run's usage", runErr: errors.New("boom"), wantOut: returned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got RunInput
			s := testNewSession(t, Dependencies{})
			s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
				got = in
				return RunResult{Conversation: in.Conversation, ParentUsage: returned}, tt.runErr
			}})

			out, err := s.driverRun(context.Background(), agent.DriverRunInput{
				Conversation: []agent.Message{{Role: agent.MessageRoleUser, Content: "hi"}},
				ParentUsage:  seed,
			}, "")
			if err != nil {
				t.Fatalf("driverRun() error = %v", err)
			}
			if !reflect.DeepEqual(got.ParentUsage, seed) {
				t.Errorf("RunInput.ParentUsage = %+v, want the driver's %+v", got.ParentUsage, seed)
			}
			if !reflect.DeepEqual(out.ParentUsage, tt.wantOut) {
				t.Errorf("DriverRunOutput.ParentUsage = %+v, want %+v", out.ParentUsage, tt.wantOut)
			}
		})
	}
}
