//go:build claudesub_probe && !unix

package provider

import (
	"errors"
	"runtime"
)

// claudeSubProbeKillProcess is unavailable off Unix: E7 needs a SIGKILL of the
// child it spawned, and this platform's production child handle exposes no
// equivalent signal. The experiment fails with this reason instead of skipping a
// measurement it never took.
func claudeSubProbeKillProcess(claudeSubConn) error {
	return errors.New("probe E7 needs a unix SIGKILL of the spawned CLI and cannot run on " + runtime.GOOS)
}
