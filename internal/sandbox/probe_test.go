package sandbox

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProbeBwrapUsable_Success(t *testing.T) {
	t.Cleanup(resetProbeCache)

	prevProbe := bwrapProbe
	bwrapProbe = func(string) error { return nil }
	t.Cleanup(func() { bwrapProbe = prevProbe })

	if err := probeBwrapUsable("/usr/bin/bwrap"); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}

func TestProbeBwrapUsable_Failure(t *testing.T) {
	t.Cleanup(resetProbeCache)

	prevProbe := bwrapProbe
	bwrapProbe = func(string) error {
		return fmt.Errorf("bwrap cannot create a namespace here")
	}
	t.Cleanup(func() { bwrapProbe = prevProbe })

	err := probeBwrapUsable("/usr/bin/bwrap")
	if err == nil {
		t.Fatal("expected non-nil error when probe fails")
	}
	if !strings.Contains(err.Error(), "bwrap cannot create a namespace") {
		t.Errorf("error = %q, want substring %q", err.Error(), "bwrap cannot create a namespace")
	}
}

func TestProbeBwrapUsable_ResultCached(t *testing.T) {
	t.Cleanup(resetProbeCache)

	var calls atomic.Int32
	prevProbe := bwrapProbe
	bwrapProbe = func(string) error {
		calls.Add(1)
		return errors.New("boom")
	}
	t.Cleanup(func() { bwrapProbe = prevProbe })

	_ = probeBwrapUsable("/usr/bin/bwrap")
	_ = probeBwrapUsable("/usr/bin/bwrap")
	if got := calls.Load(); got != 1 {
		t.Errorf("bwrapProbe ran %d times, want 1 (probe result must be cached)", got)
	}
}
