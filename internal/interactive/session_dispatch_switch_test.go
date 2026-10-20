package interactive

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
)

func TestSwitchModelRecorderFailureWarnsAndSucceeds(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
		RecordModelSwitch: func(string, string) error {
			return errors.New("stats disk full")
		},
		Config: config.Config{
			Providers: map[string]config.ProviderConfig{"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://example/v1"}},
			Models: config.ModelsConfig{
				Effective: config.EffectiveModelAssignments{DefaultModel: "current", ActiveOrchestratorModel: "current"},
				Definitions: map[string]config.ModelConfig{
					"current": {Provider: "local", ID: "current-id"},
					"fast":    {Provider: "local", ID: "fast-id"},
				},
			},
		},
	})

	if err := s.Handle(context.Background(), SwitchModel{Name: "fast"}); err != nil {
		t.Fatalf("Handle(SwitchModel) = %v, want nil", err)
	}
	if got := s.deps.Config.Models.Effective.ActiveOrchestratorModel; got != "fast" {
		t.Fatalf("config active_orchestrator_model = %q, want %q", got, "fast")
	}

	var warnings []string
	for _, event := range events {
		if payload, ok := event.Payload.(output.ConfigWarningEvent); ok {
			warnings = append(warnings, payload.Message)
		}
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "stats disk full") {
		t.Fatalf("config warnings = %q, want one warning containing the recorder error", warnings)
	}
}
