//go:build claudesub_probe && unix

package provider

import (
	"errors"
	"fmt"
	"syscall"
)

// claudeSubProbeKillProcess SIGKILLs the CLI child E7 spawned. It goes through the
// connection the spawn seam returned and the production child handle it wraps, so
// it can only ever signal the process this probe started, and it signals that
// process alone rather than its group: the coordinator's own tree termination
// still runs at teardown.
func claudeSubProbeKillProcess(conn claudeSubConn) error {
	process, ok := conn.(*claudeSubProcess)
	if !ok {
		return errors.New("probe connection is not a production claude CLI process")
	}
	child, ok := process.child.(*claudeSubUnixChild)
	if !ok {
		return errors.New("probe child is not a unix claude CLI process")
	}
	if child.cmd.Process == nil {
		return errors.New("probe child has no process handle to signal")
	}
	if err := child.cmd.Process.Signal(syscall.SIGKILL); err != nil {
		return fmt.Errorf("signal probe child with SIGKILL: %w", err)
	}
	return nil
}
