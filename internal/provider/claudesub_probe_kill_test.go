//go:build claudesub_probe && unix

package provider

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
)

// claudeSubProbeOtherChild is a child handle that is not the unix process E7
// spawned, so the kill seam must refuse it instead of signalling something else.
type claudeSubProbeOtherChild struct{}

func (claudeSubProbeOtherChild) exited() <-chan struct{} { return nil }
func (claudeSubProbeOtherChild) terminateTree() error    { return nil }
func (claudeSubProbeOtherChild) wait() error             { return nil }
func (claudeSubProbeOtherChild) close()                  {}

// claudeSubProbeKillAvailable reports that this platform has the kill seam E7
// needs.
const claudeSubProbeKillAvailable = true

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

// TestClaudeSubProbeKillProcessTypeRejection checks that the kill seam signals only
// a production process whose child it recognises: any other connection or child is
// refused, so the probe can never signal a process it did not spawn.
func TestClaudeSubProbeKillProcessTypeRejection(t *testing.T) {
	cases := []struct {
		name string
		conn claudeSubConn
		want string
	}{
		{name: "foreign connection", conn: newClaudeSubFakeConn(), want: "not a production claude CLI process"},
		{name: "foreign child", conn: &claudeSubProcess{child: claudeSubProbeOtherChild{}}, want: "not a unix claude CLI process"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := claudeSubProbeKillProcess(tc.conn)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("kill error = %v, want %q", err, tc.want)
			}
		})
	}
}
