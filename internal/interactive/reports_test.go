package interactive

import (
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

func TestBuildConfigReportFormatsResolvedYAML(t *testing.T) {
	t.Parallel()
	report, err := buildConfigReport(config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: config.ModelsConfig{
			Effective: config.EffectiveModelAssignments{DefaultModel: "mymodel", ActiveOrchestratorModel: "mymodel"},
			Definitions: map[string]config.ModelConfig{
				"mymodel": {Provider: "local", ID: "qwen"},
			},
		},
		Logging: config.LoggingConfig{
			Enabled: true,
			Level:   "debug",
		},
	})
	if err != nil {
		t.Fatalf("buildConfigReport() error = %v", err)
	}
	if !strings.HasPrefix(report, "```yaml\n") {
		t.Fatalf("report prefix = %q, want yaml fence", report)
	}
	for _, want := range []string{"models:", "definitions:", "base_url: http://localhost:11434/v1", "id: qwen", "logging:"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report = %q, want %q", report, want)
		}
	}
	if !strings.HasSuffix(report, "\n```") {
		t.Fatalf("report suffix = %q, want closing fence", report)
	}
}

func TestRequestContextReportWithSnapshot(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s, err := NewSession(Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	s.snapshots.Store(RequestContextSnapshot{
		Model: "test-model",
	})

	handleErr := s.Handle(context.Background(), RequestContextReport{})
	if handleErr != nil {
		t.Fatalf("Handle(RequestContextReport) = %v, want nil", handleErr)
	}

	if len(events) < 1 {
		t.Fatal("expected at least one event")
	}
	reportEvent, ok := events[0].Payload.(output.ContextReportEvent)
	if !ok {
		t.Fatalf("event payload = %T, want output.ContextReportEvent", events[0].Payload)
	}
	if !strings.Contains(reportEvent.Content, "test-model") {
		t.Fatalf("report content = %q, want test-model", reportEvent.Content)
	}
	if !strings.Contains(reportEvent.Content, "Prompt tokens") {
		t.Fatalf("report content = %q, want Prompt tokens", reportEvent.Content)
	}
}

func TestRequestContextReportNoSnapshot(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s, err := NewSession(Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	handleErr := s.Handle(context.Background(), RequestContextReport{})
	if handleErr != nil {
		t.Fatalf("Handle(RequestContextReport) = %v, want nil", handleErr)
	}

	if len(events) < 1 {
		t.Fatal("expected at least one event")
	}
	reportEvent, ok := events[0].Payload.(output.ContextReportEvent)
	if !ok {
		t.Fatalf("event payload = %T, want output.ContextReportEvent", events[0].Payload)
	}
	if !strings.Contains(reportEvent.Content, "No request recorded yet") {
		t.Fatalf("report content = %q, want 'No request recorded yet'", reportEvent.Content)
	}
}

func TestRequestContextReportBuildError(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s, err := NewSession(Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	s.snapshots.Store(RequestContextSnapshot{
		Model: "test-model",
		Messages: []provider.Message{
			{Role: provider.MessageRoleUser, Content: "hello"},
		},
	})

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	handleErr := s.Handle(cancelledCtx, RequestContextReport{})
	if handleErr != nil {
		t.Fatalf("Handle(RequestContextReport) = %v, want nil", handleErr)
	}

	if len(events) < 1 {
		t.Fatal("expected at least one event")
	}
	reportEvent, ok := events[0].Payload.(output.ContextReportEvent)
	if !ok {
		t.Fatalf("event payload = %T, want output.ContextReportEvent", events[0].Payload)
	}
	if !strings.Contains(reportEvent.Content, "Context report unavailable") {
		t.Fatalf("report content = %q, want 'Context report unavailable'", reportEvent.Content)
	}
}

func TestRequestConfigReportSuccess(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s, err := NewSession(Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
		Config: config.Config{
			Models: config.ModelsConfig{
				Effective:   config.EffectiveModelAssignments{DefaultModel: "mymodel", ActiveOrchestratorModel: "mymodel"},
				Definitions: map[string]config.ModelConfig{"mymodel": {Provider: "local", ID: "gpt-test"}},
			},
			Providers: map[string]config.ProviderConfig{"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://test/v1"}},
		},
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	handleErr := s.Handle(context.Background(), RequestConfigReport{})
	if handleErr != nil {
		t.Fatalf("Handle(RequestConfigReport) = %v, want nil", handleErr)
	}

	if len(events) < 1 {
		t.Fatal("expected at least one event")
	}
	reportEvent, ok := events[0].Payload.(output.ContextReportEvent)
	if !ok {
		t.Fatalf("event payload = %T, want output.ContextReportEvent", events[0].Payload)
	}
	if !strings.Contains(reportEvent.Content, "gpt-test") {
		t.Fatalf("report content = %q, want 'gpt-test'", reportEvent.Content)
	}
	if !strings.HasPrefix(reportEvent.Content, "```yaml\n") {
		t.Fatalf("report prefix = %q, want yaml fence", reportEvent.Content)
	}
}

func TestRequestConfigReportWithZeroConfig(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s, err := NewSession(Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	handleErr := s.Handle(context.Background(), RequestConfigReport{})
	if handleErr != nil {
		t.Fatalf("Handle(RequestConfigReport) = %v, want nil", handleErr)
	}

	if len(events) < 1 {
		t.Fatal("expected at least one event")
	}
	_, ok := events[0].Payload.(output.ContextReportEvent)
	if !ok {
		t.Fatalf("event payload = %T, want output.ContextReportEvent", events[0].Payload)
	}
}
