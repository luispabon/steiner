package provider

import (
	"context"
	"testing"
)

// The !useDiscovery path in resolveReferenceWithLoader drops resolveFacts'
// sourceErrs. This pins the reason: its sources never report a source error.
func TestConfigPathSourcesReportNoSourceErrors(t *testing.T) {
	_, _, sourceErrs := resolveFacts(context.Background(), modelRef{}, []factSource{configSource{}, providerFixedSource{}, builtinSource{}})
	if len(sourceErrs) != 0 {
		t.Fatalf("sourceErrs = %q, want none on the config-only path", sourceErrs)
	}
}
