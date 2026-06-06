package sandbox

import (
	"errors"
	"fmt"
	"os"
)

const (
	sshSystemConfigPath = "/etc/ssh/ssh_config"
	sshMaxIncludeDepth  = 8
	sshMaxFiles         = 128
	sshMaxFileBytes     = 1 << 20
	sshMaxTotalBytes    = 8 << 20
)

// sshOverlay owns the generated bwrap args and any memfd-backed files.
type sshOverlay struct {
	bwrapArgs []string
	memfds    []*os.File
}

// Close releases any open memfd files owned by the overlay.
func (o *sshOverlay) Close() error {
	if o == nil {
		return nil
	}

	var errs []error
	for _, f := range o.memfds {
		if f == nil {
			continue
		}
		if err := f.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close ssh overlay memfd: %w", err))
		}
	}

	o.memfds = nil

	return errors.Join(errs...)
}

// sshOverlayFile describes a resolved SSH file in the sandbox.
type sshOverlayFile struct {
	sourcePath  string
	sandboxPath string
	content     []byte
	memfd       *os.File
}

// sshIncludeResolution is the result of resolving SSH include directives.
type sshIncludeResolution struct {
	files              []sshOverlayFile
	replacementDirs    []string
	skippedDiagnostics []string
}
