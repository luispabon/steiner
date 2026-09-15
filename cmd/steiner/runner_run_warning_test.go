package main

import "testing"

func TestHasMetadataDegradationWarning(t *testing.T) {
	if hasMetadataDegradationWarning([]string{"unrelated configuration warning"}) {
		t.Fatal("unrelated warning reported as metadata degradation")
	}
	if !hasMetadataDegradationWarning([]string{"Model metadata warning: models.dev lookup degraded: provider_mismatch."}) {
		t.Fatal("metadata degradation warning not detected")
	}
}
