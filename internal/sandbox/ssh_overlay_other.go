//go:build !linux

package sandbox

import "os"

const sshSystemConfigPath = "/etc/ssh/ssh_config"

type sshOverlay struct {
	bwrapArgs []string
	memfds    []*os.File
}

func (o *sshOverlay) Close() error {
	return nil
}

func prepareSSHOverlayFromPath(rootPath string, childFDBase int) (*sshOverlay, error) {
	return nil, nil
}
