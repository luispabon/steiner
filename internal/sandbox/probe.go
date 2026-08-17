package sandbox

import (
	"fmt"
	"os/exec"
	"sync"
)

// bwrapProbe runs a minimal namespace-creating bwrap invocation and returns an
// error when bwrap cannot create a namespace (e.g. inside a nested sandbox or
// when unprivileged user namespaces are disabled). It is a variable so tests
// can stub it.
var bwrapProbe = func(bwrapPath string) error {
	out, err := exec.Command(bwrapPath, "--ro-bind", "/", "/", "true").CombinedOutput() //nolint:noctx
	if err != nil {
		return fmt.Errorf("bwrap cannot create a namespace: %w: %s", err, out)
	}
	return nil
}

// probeOnce and probeResult cache the first probe outcome so PrereqCheck stays
// cheap across repeated calls.
var (
	probeOnce   sync.Once
	probeResult error
)

// probeBwrapUsable returns nil when bwrap can create a namespace on this host,
// or a descriptive error. The result is cached after the first call.
func probeBwrapUsable(bwrapPath string) error {
	probeOnce.Do(func() {
		probeResult = bwrapProbe(bwrapPath)
	})
	return probeResult
}

// resetProbeCache clears the cached probe result so the next probeBwrapUsable
// call re-runs bwrapProbe. Test-only.
func resetProbeCache() {
	probeOnce = sync.Once{}
	probeResult = nil
}
