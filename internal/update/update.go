package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// ErrUpToDate is returned by Update when the current version is already up to
// date with the latest release.
var ErrUpToDate = fmt.Errorf("already up to date")

// assetName returns the expected asset name for the current OS and
// architecture.
func assetName() string {
	name := fmt.Sprintf("steiner-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// checksumsFileName returns the expected checksums file name for a given
// version (without leading "v").
func checksumsFileName(version string) string {
	return fmt.Sprintf("steiner_%s_checksums.txt", version)
}

// version holds a parsed semver.
type version struct {
	major int
	minor int
	patch int
}

// parseVersion parses a semver string, stripping an optional leading "v" or
// "V" prefix.
func parseVersion(s string) (version, error) {
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")

	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return version{}, fmt.Errorf("invalid semver: %q", s)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return version{}, fmt.Errorf("invalid major version %q: %w", parts[0], err)
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return version{}, fmt.Errorf("invalid minor version %q: %w", parts[1], err)
	}

	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return version{}, fmt.Errorf("invalid patch version %q: %w", parts[2], err)
	}

	return version{major: major, minor: minor, patch: patch}, nil
}

// isOlderThan returns true if v is strictly older than other.
func (v version) isOlderThan(other version) bool {
	if v.major != other.major {
		return v.major < other.major
	}
	if v.minor != other.minor {
		return v.minor < other.minor
	}
	return v.patch < other.patch
}

// findAsset searches release assets for one matching the expected binary name.
// It returns the matching asset or nil.
func findAsset(release *Release, name string) *Asset {
	for i := range release.Assets {
		if release.Assets[i].Name == name {
			return &release.Assets[i]
		}
	}
	return nil
}

// findChecksumAsset searches release assets for a checksums file matching the
// given version.
func findChecksumAsset(release *Release, version string) *Asset {
	fileName := checksumsFileName(version)
	for i := range release.Assets {
		if release.Assets[i].Name == fileName {
			return &release.Assets[i]
		}
	}
	return nil
}

// parseChecksums parses a checksums file in the format "<hex>  <filename>"
// (separated by two spaces) and returns a map from filename to hex checksum.
func parseChecksums(data []byte) (map[string]string, error) {
	checksums := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Format: "<hex>  <filename>" with two spaces.
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			continue // skip malformed lines
		}
		checksum := strings.TrimSpace(parts[0])
		filename := strings.TrimSpace(parts[1])
		checksums[filename] = checksum
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan checksums: %w", err)
	}
	return checksums, nil
}

// verifyChecksum checks whether data matches the expected hex-encoded SHA256
// checksum.
func verifyChecksum(data []byte, expectedHex string) error {
	expected, err := hex.DecodeString(expectedHex)
	if err != nil {
		return fmt.Errorf("decode expected checksum: %w", err)
	}

	sum := sha256.Sum256(data)
	if !bytes.Equal(sum[:], expected) {
		return fmt.Errorf("checksum mismatch: got %x, expected %s", sum[:], expectedHex)
	}
	return nil
}

// replaceBinary atomically replaces the running executable with new bytes.
//
// It renames the current binary to <path>.old, writes new content to
// <path>.tmp, then renames <path>.tmp to the target path. On failure after the
// first rename it attempts to roll back. On success it removes the .old file
// best-effort.
func replaceBinary(exePath string, newData []byte) (retErr error) {
	oldPath := exePath + ".old"
	tmpPath := exePath + ".tmp"

	// Rename current binary to .old.
	if err := os.Rename(exePath, oldPath); err != nil {
		return fmt.Errorf("rename executable to .old: %w", err)
	}

	// On any failure after the first rename, attempt rollback.
	defer func() {
		if retErr != nil {
			_ = os.Rename(oldPath, exePath)
		}
	}()

	// Write new binary to .tmp.
	if err := os.WriteFile(tmpPath, newData, 0o755); err != nil {
		return fmt.Errorf("write temp binary: %w", err)
	}

	// Rename .tmp to target.
	if err := os.Rename(tmpPath, exePath); err != nil {
		// Attempt to clean up .tmp before rollback
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp to executable: %w", err)
	}

	// Success: remove .old best-effort.
	if err := os.Remove(oldPath); err != nil {
		// On Windows, try to hide the .old file instead.
		if runtime.GOOS == "windows" {
			_ = hideFile(oldPath)
		}
	}

	return nil
}

// Update checks the latest release of steiner on GitHub, downloads a matching
// binary for the current platform, verifies its checksum, and atomically
// replaces the running executable.
//
// currentVersion should be the current semver string (with or without "v"
// prefix). owner and repo identify the GitHub repository. If token is
// non-empty, it is used as a Bearer token for GitHub API requests.
func Update(ctx context.Context, currentVersion, owner, repo, token string) error {
	// Fetch the latest release.
	release, err := fetchLatestRelease(ctx, owner, repo, token)
	if err != nil {
		return fmt.Errorf("fetch latest release: %w", err)
	}

	// Compare versions.
	latestTag := release.TagName
	latestVer, err := parseVersion(latestTag)
	if err != nil {
		return fmt.Errorf("parse latest version %q: %w", latestTag, err)
	}

	currVer, err := parseVersion(currentVersion)
	if err != nil {
		return fmt.Errorf("parse current version %q: %w", currentVersion, err)
	}

	if !currVer.isOlderThan(latestVer) {
		return ErrUpToDate
	}

	// Find the matching asset.
	name := assetName()
	asset := findAsset(release, name)
	if asset == nil {
		return fmt.Errorf("no asset found for %s in release %s", name, latestTag)
	}

	// Find the checksums asset.
	strippedTag := strings.TrimPrefix(latestTag, "v")
	strippedTag = strings.TrimPrefix(strippedTag, "V")
	checksumAsset := findChecksumAsset(release, strippedTag)
	if checksumAsset == nil {
		return fmt.Errorf("no checksums file found for version %s", strippedTag)
	}

	// Download the binary.
	binaryData, err := downloadAsset(ctx, asset.DownloadURL, token)
	if err != nil {
		return fmt.Errorf("download asset: %w", err)
	}

	// Download checksums.
	checksumData, err := downloadChecksums(ctx, checksumAsset.DownloadURL, token)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}

	// Parse checksums.
	checksums, err := parseChecksums(checksumData)
	if err != nil {
		return fmt.Errorf("parse checksums: %w", err)
	}

	// Verify the binary.
	expectedHex, ok := checksums[name]
	if !ok {
		return fmt.Errorf("no checksum found for %s in checksums file", name)
	}

	if err := verifyChecksum(binaryData, expectedHex); err != nil {
		return fmt.Errorf("verify binary: %w", err)
	}

	// Get the running executable path.
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	// Resolve symlinks to get the real path.
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}

	// Replace the binary.
	if err := replaceBinary(exePath, binaryData); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}

	return nil
}
