package sandbox

import (
	"os"
	"path/filepath"

	"github.com/luispabon/steiner/internal/config"
)

// BuildArgs returns the bwrap argument list (excluding the trailing -- cmd args).
func BuildArgs(workspace, sandboxHome, userHome string, perms config.PermissionsConfig, hostMounts []config.HostMount) []string {
	var args []string

	// Namespace isolation: unshare all but share network.
	args = append(args, "--unshare-all", "--share-net")

	// Core mounts.
	args = append(args,
		"--bind", workspace, "/workspace",
		"--bind", sandboxHome, "/home/steiner",
		"--tmpfs", "/tmp",
		"--proc", "/proc",
		"--dev", "/dev",
		"--ro-bind", "/usr", "/usr",
	)

	// Optional system dirs that may not exist on all distros.
	for _, dir := range []string{"/bin", "/lib", "/lib64"} {
		if pathExists(dir) {
			args = append(args, "--ro-bind", dir, dir)
		}
	}

	// Network and TLS files.
	args = append(args,
		"--ro-bind", "/etc/resolv.conf", "/etc/resolv.conf",
		"--ro-bind", "/etc/hosts", "/etc/hosts",
	)
	if pathExists("/etc/ssl/certs") {
		args = append(args, "--ro-bind", "/etc/ssl/certs", "/etc/ssl/certs")
	}

	// User credential files from host home.
	sshDir := filepath.Join(userHome, ".ssh")
	if pathExists(sshDir) {
		args = append(args, "--ro-bind", sshDir, "/home/steiner/.ssh")
	}

	gitcfg := filepath.Join(userHome, ".gitconfig")
	if pathExistsFile(gitcfg) {
		args = append(args, "--ro-bind", gitcfg, "/home/steiner/.gitconfig")
	}

	gitcreds := filepath.Join(userHome, ".git-credentials")
	if pathExistsFile(gitcreds) {
		args = append(args, "--ro-bind", gitcreds, "/home/steiner/.git-credentials")
	}

	gitConfigDir := filepath.Join(userHome, ".config", "git")
	if pathExistsDir(gitConfigDir) {
		args = append(args, "--ro-bind", gitConfigDir, "/home/steiner/.config/git")
	}

	awsDir := filepath.Join(userHome, ".aws")
	if pathExists(awsDir) {
		args = append(args, "--ro-bind", awsDir, "/home/steiner/.aws")
	}

	kubeDir := filepath.Join(userHome, ".kube")
	if pathExists(kubeDir) {
		args = append(args, "--ro-bind", kubeDir, "/home/steiner/.kube")
	}

	dockerCfg := filepath.Join(userHome, ".docker", "config.json")
	if pathExistsFile(dockerCfg) {
		args = append(args, "--ro-bind", dockerCfg, "/home/steiner/.docker/config.json")
	}

	// SSH agent socket.
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" && pathExists(sock) {
		args = append(args, "--ro-bind", sock, sock)
	}

	// Additional host mounts from config.
	for _, hm := range hostMounts {
		flag := "--ro-bind"
		if hm.Mode == "rw" {
			flag = "--bind"
		}
		args = append(args, flag, hm.Path, hm.Path)
	}

	// Override HOME inside the sandbox.
	args = append(args, "--setenv", "HOME", "/home/steiner")

	return args
}

// pathExists returns true if path exists (file or directory).
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// pathExistsFile returns true if path exists and is a regular file.
func pathExistsFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// pathExistsDir returns true if path exists and is a directory.
func pathExistsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
