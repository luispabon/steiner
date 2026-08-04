package sandbox

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestPrereqCheck_WhenBwrapInstalled(t *testing.T) {
	prevLookup := lookupBwrap
	lookupBwrap = func(_ string) (string, error) {
		return "/usr/bin/bwrap", nil
	}
	t.Cleanup(func() { lookupBwrap = prevLookup })

	if err := PrereqCheck(); err != nil {
		t.Errorf("expected PrereqCheck to return nil when bwrap is installed, got: %v", err)
	}
}

func TestPrereqCheck_ErrorMessage(t *testing.T) {
	prevLookup := lookupBwrap
	lookupBwrap = func(_ string) (string, error) {
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { lookupBwrap = prevLookup })

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

func TestIsSupportedPlatform(t *testing.T) {
	result := IsSupportedPlatform()
	if result && runtime.GOOS != "linux" {
		t.Errorf("IsSupportedPlatform() = true on non-Linux OS %q", runtime.GOOS)
	}
	if !result && runtime.GOOS == "linux" {
		t.Errorf("IsSupportedPlatform() = false on Linux")
	}
}
