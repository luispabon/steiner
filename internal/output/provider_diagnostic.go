package output

import (
	"fmt"
	"time"
)

// NewProviderDiagnosticEvent creates a diagnostic event emitted from provider
// retry handling and similar transport-layer concerns.
func NewProviderDiagnosticEvent(payload ProviderDiagnosticEvent) Event {
	return Event{
		Type:      EventTypeProviderDiagnostic,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

// NewTransportDiagnosticEvent creates a diagnostic event when a model's
// effective transport differs from the configured provider type.
func NewTransportDiagnosticEvent(model, configuredType, effectiveType, metadataSource, reason string) Event {
	return NewProviderDiagnosticEvent(ProviderDiagnosticEvent{
		Severity:     "info",
		Kind:         ProviderDiagnosticKindTransportOverride,
		Suppressible: true,
		Message:      fmt.Sprintf("model %s uses %s transport (was %s) from %s (%s)", model, effectiveType, configuredType, metadataSource, reason),
	})
}

func (payload ProviderDiagnosticEvent) suppressInTranscript() bool {
	return payload.Severity == "info" && payload.Suppressible && payload.Kind == ProviderDiagnosticKindTransportOverride
}
