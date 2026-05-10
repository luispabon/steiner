package output

import "time"

// NewProviderDiagnosticEvent creates a diagnostic event emitted from provider
// retry handling and similar transport-layer concerns.
func NewProviderDiagnosticEvent(payload ProviderDiagnosticEvent) Event {
	return Event{
		Type:      EventTypeProviderDiagnostic,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}
