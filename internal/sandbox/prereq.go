package sandbox

import (
	"fmt"
	"runtime"
)

// IsSupportedPlatform returns true when the host OS is Linux, which is the only
// platform bubblewrap supports.
func IsSupportedPlatform() bool {
	return runtime.GOOS == "linux"
}

// PrereqCheck returns nil if the platform is supported and bwrap is found on
// PATH and can create a namespace, or a descriptive error with install
// instructions.
func PrereqCheck() error {
	if !IsSupportedPlatform() {
		return fmt.Errorf(
			"unsupported platform: bubblewrap requires Linux, current OS is %s",
			runtime.GOOS,
		)
	}
	bwrapPath, err := lookupBwrap("bwrap")
	if err != nil {
		return fmt.Errorf(
			"bwrap not found on PATH: install bubblewrap: apt install bubblewrap / dnf install bubblewrap",
		)
	}
	if err := probeBwrapUsable(bwrapPath); err != nil {
		return fmt.Errorf("bwrap found but unusable: %w", err)
	}
	return nil
}
