package delegation

import (
	"errors"
	"strings"
	"testing"
)

func TestSetupErrorProjectToolErrorAlreadyActive(t *testing.T) {
	envelope := (&SetupError{err: ErrAgentAlreadyActive}).ProjectToolError()
	if envelope.Output != "" {
		t.Errorf("Output = %q, want empty", envelope.Output)
	}
	if envelope.Status != "failed" {
		t.Errorf("Status = %q, want failed", envelope.Status)
	}
	if !strings.Contains(envelope.Reason, "already has a call in flight") {
		t.Errorf("Reason = %q, want already-active guidance", envelope.Reason)
	}
}

func TestSetupErrorProjectToolErrorGeneric(t *testing.T) {
	envelope := (&SetupError{err: errors.New("other")}).ProjectToolError()
	if envelope.Output != "" {
		t.Errorf("Output = %q, want empty", envelope.Output)
	}
	if envelope.Status != "failed" {
		t.Errorf("Status = %q, want failed", envelope.Status)
	}
	if envelope.Reason != "child setup failed" {
		t.Errorf("Reason = %q, want child setup failed", envelope.Reason)
	}
}

func TestSetupErrorProjectToolErrorRequiresCommit(t *testing.T) {
	envelope := (&SetupError{err: ErrCodeWorktreeRequiresCommit}).ProjectToolError()
	want := "code sub-agent requires a Git repository with at least one commit; commit the project files and retry"
	if envelope.Reason != want {
		t.Fatalf("Reason = %q, want %q", envelope.Reason, want)
	}
}
