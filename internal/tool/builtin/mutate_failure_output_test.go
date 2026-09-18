package builtin

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildMultiFailureOutputHasTotalBudget(t *testing.T) {
	failures := make([]mutateFailure, 0, 100)
	for i := 0; i < 100; i++ {
		failures = append(failures, mutateFailure{
			index:  i + 1,
			opType: "replace",
			path:   "file.txt",
			err:    errors.New(strings.Repeat("é", 5000)),
		})
	}

	got := buildMultiFailureOutput(failures, len(failures))
	if len(got) > maxMutateFailureOutputSize {
		t.Fatalf("buildMultiFailureOutput() length = %d, want <= %d", len(got), maxMutateFailureOutputSize)
	}
	if !utf8.ValidString(got) {
		t.Fatal("buildMultiFailureOutput() returned invalid UTF-8")
	}
	if !strings.HasSuffix(got, diagnosticTruncationMarker) {
		t.Fatalf("buildMultiFailureOutput() does not end with truncation marker")
	}
}
