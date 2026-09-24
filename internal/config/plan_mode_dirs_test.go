package config

import (
	"reflect"
	"testing"
)

func TestPlanModeWritableDirs(t *testing.T) {
	t.Parallel()
	want := []string{".steiner/plans", ".steiner/security"}

	got := PlanModeWritableDirs()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PlanModeWritableDirs() = %#v, want %#v", got, want)
	}

	// Mutating the returned slice must not affect later calls.
	got[0] = "mutated"
	if again := PlanModeWritableDirs(); !reflect.DeepEqual(again, want) {
		t.Fatalf("PlanModeWritableDirs() after caller mutation = %#v, want %#v", again, want)
	}
}
