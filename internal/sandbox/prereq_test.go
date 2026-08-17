package sandbox

import (
	"fmt"
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

	prevProbe := bwrapProbe
	bwrapProbe = func(string) error { return nil }
	t.Cleanup(func() { bwrapProbe = prevProbe })
	t.Cleanup(resetProbeCache)

	if err := PrereqCheck(); err != nil {
		t.Errorf("expected PrereqCheck to return nil when bwrap is installed, got: %v", err)
	}
}

func TestPrereqCheck_BwrapExistsButUnusable(t *testing.T) {
	prevLookup := lookupBwrap
	lookupBwrap = func(_ string) (string, error) {
		return "/usr/bin/bwrap", nil
	}
	t.Cleanup(func() { lookupBwrap = prevLookup })

	prevProbe := bwrapProbe
	bwrapProbe = func(string) error {
		return fmt.Errorf("bwrap cannot create a namespace here")
	}
	t.Cleanup(func() { bwrapProbe = prevProbe })
	t.Cleanup(resetProbeCache)

	err := PrereqCheck()
	if err == nil {
		t.Fatal("expected non-nil error when bwrap exists but cannot create a namespace")
	}
	if !strings.Contains(err.Error(), "bwrap found but unusable") {
		t.Errorf("error message missing 'bwrap found but unusable': %q", err.Error())
	}
}

func TestPrereqCheck_BwrapUsable(t *testing.T) {
	prevLookup := lookupBwrap
	lookupBwrap = func(_ string) (string, error) {
		return "/usr/bin/bwrap", nil
	}
	t.Cleanup(func() { lookupBwrap = prevLookup })

	prevProbe := bwrapProbe
	bwrapProbe = func(string) error { return nil }
	t.Cleanup(func() { bwrapProbe = prevProbe })
	t.Cleanup(resetProbeCache)

	if err := PrereqCheck(); err != nil {
		t.Errorf("expected PrereqCheck to return nil when bwrap is usable, got: %v", err)
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
