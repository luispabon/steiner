//go:build !unix

package tool

import (
	"os/exec"
	"time"
)

// applySubprocessGroup bounds Wait after cancellation; process groups are not
// available on this platform.
func applySubprocessGroup(cmd *exec.Cmd) {
	cmd.WaitDelay = 5 * time.Second
}
