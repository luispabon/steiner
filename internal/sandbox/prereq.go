package sandbox

import (
	"fmt"
	"os/exec"
)

// PrereqCheck returns nil if bwrap is found on PATH, or a descriptive error
// with install instructions.
func PrereqCheck() error {
	_, err := exec.LookPath("bwrap")
	if err != nil {
		return fmt.Errorf(
			"bwrap not found on PATH: install bubblewrap: apt install bubblewrap / dnf install bubblewrap",
		)
	}
	return nil
}
