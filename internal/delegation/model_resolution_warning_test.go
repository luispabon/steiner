package delegation

import (
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

func TestBuildModelResolverEmitsResolvedModelWarningsOncePerAlias(t *testing.T) {
	warning := "Model metadata warning: luna/luna-v1 has unknown context limits. Using conservative fallback: context_window=32768, max_output_tokens=4096. Set models.luna.advanced.limits.context_window to remove this warning."
	sink := &recordingEventSink{}
	resolver := buildModelResolverWithResolve(DelegateDeps{Events: sink}, func(alias string) (provider.ResolvedModel, error) {
		return provider.ResolvedModel{Alias: alias, BackendModelID: "luna-v1", Warnings: []string{warning}}, nil
	})

	for _, alias := range []string{" luna ", "luna", " luna"} {
		if _, _, err := resolver(alias); err != nil {
			t.Fatalf("resolver(%q) error = %v", alias, err)
		}
	}

	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("warning events = %d, want 1", len(events))
	}
	if events[0].Type != output.EventTypeConfigWarning {
		t.Fatalf("event type = %q, want %q", events[0].Type, output.EventTypeConfigWarning)
	}
	payload, ok := events[0].Payload.(output.ConfigWarningEvent)
	if !ok {
		t.Fatalf("event payload = %T, want output.ConfigWarningEvent", events[0].Payload)
	}
	if payload.Message != warning {
		t.Fatalf("warning message = %q, want %q", payload.Message, warning)
	}
}

func TestBuildModelResolverEmitsWarningsForDifferentAliasesIndependently(t *testing.T) {
	sink := &recordingEventSink{}
	warnings := map[string][]string{
		"luna": {"warning for models.luna.advanced.limits.context_window"},
		"nova": {"warning for models.nova.advanced.limits.context_window"},
	}
	resolver := buildModelResolverWithResolve(DelegateDeps{Events: sink}, func(alias string) (provider.ResolvedModel, error) {
		return provider.ResolvedModel{Alias: alias, Warnings: warnings[alias]}, nil
	})

	for _, alias := range []string{"luna", "nova", "luna"} {
		if _, _, err := resolver(alias); err != nil {
			t.Fatalf("resolver(%q) error = %v", alias, err)
		}
	}

	var got []string
	for _, event := range sink.Events() {
		payload, ok := event.Payload.(output.ConfigWarningEvent)
		if !ok {
			t.Fatalf("event payload = %T, want output.ConfigWarningEvent", event.Payload)
		}
		got = append(got, payload.Message)
	}
	want := []string{warnings["luna"][0], warnings["nova"][0]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("warning messages = %v, want %v", got, want)
	}
}
