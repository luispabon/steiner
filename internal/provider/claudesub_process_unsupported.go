//go:build unix && !linux && !android && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package provider

import (
	"fmt"
	"runtime"
)

// newClaudeSubExitObserver is unavailable on this Unix target: there is no
// non-reaping process-exit observer, so the claude CLI transport refuses to
// start rather than run without safe tree cleanup.
func newClaudeSubExitObserver(int) (claudeSubExitObserver, error) {
	return nil, fmt.Errorf("claude CLI transport is unsupported on %s: no non-reaping process-exit observer is available", runtime.GOOS)
}
