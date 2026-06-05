package delegation

import (
	"errors"
	"strings"
	"testing"
)

func TestFailedDelegateSummaryText_NoPreviousOutput(t *testing.T) {
	err := errors.New("deadline exceeded")
	summary := failedDelegateSummaryText(err, "")
	if !strings.Contains(summary, "delegation failed: deadline exceeded") {
		t.Fatalf("expected failure summary, got: %s", summary)
	}
}

func TestFailedDelegateSummaryText_OutputWins(t *testing.T) {
	err := errors.New("deadline exceeded")
	summary := failedDelegateSummaryText(err, "found 3 issues in pkg A")
	if !strings.Contains(summary, "previous output:") {
		t.Errorf("expected previous output in summary, got: %s", summary)
	}
}
