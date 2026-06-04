package sandbox

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPrereqCheck_WhenBwrapInstalled(t *testing.T) {
	_, err := exec.LookPath("bwrap")
	if err != nil {
		// bwrap not installed — skip this half of the table.
		t.Skip("bwrap not installed, skipping installed check")
	}

	if err := PrereqCheck(); err != nil {
		t.Errorf("expected PrereqCheck to return nil when bwrap is installed, got: %v", err)
	}
}

func TestPrereqCheck_ErrorMessage(t *testing.T) {
	_, err := exec.LookPath("bwrap")
	if err == nil {
		// bwrap is installed — can't test the error branch directly,
		// but we can verify the function contract is satisfied.
		t.Skip("bwrap is installed, cannot test missing-bwrap error message")
	}

	checkErr := PrereqCheck()
	if checkErr == nil {
		t.Fatal("expected non-nil error when bwrap is not on PATH")
	}

	msg := checkErr.Error()
	if !strings.Contains(msg, "bwrap not found on PATH") {
		t.Errorf("error message missing 'bwrap not found on PATH': %q", msg)
	}
	if !strings.Contains(msg, "install bubblewrap") {
		t.Errorf("error message missing 'install bubblewrap': %q", msg)
	}
	if !strings.Contains(msg, "apt install bubblewrap") {
		t.Errorf("error message missing 'apt install bubblewrap': %q", msg)
	}
	if !strings.Contains(msg, "dnf install bubblewrap") {
		t.Errorf("error message missing 'dnf install bubblewrap': %q", msg)
	}
}
