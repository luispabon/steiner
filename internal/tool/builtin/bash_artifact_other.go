//go:build !unix

package builtin

import "errors"

// saveBashArtifactPlatform fails explicitly: race-free no-follow directory
// traversal is only implemented for unix.
func saveBashArtifactPlatform(_ string, _ []byte) (string, error) {
	return "", errors.New("bash artifact storage is not supported on this platform")
}
