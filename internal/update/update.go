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

// osExecutable is set to os.Executable by default and can be replaced in tests.
var osExecutable = os.Executable

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

// leadingDigits returns the contiguous leading digit runes from s.
// If s has no leading digits, it returns an empty string.
func leadingDigits(s string) string {
	for i, r := range s {
		if r < '0' || r > '9' {
			return s[:i]
		}
	}
	return s
}

// parseVersion parses a semver string, stripping an optional leading "v" or
// "V" prefix and discarding any non-numeric suffix on each component (e.g.
// pre-release or build metadata like "-8-g8bd663f").
func parseVersion(s string) (version, error) {
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")

	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return version{}, fmt.Errorf("invalid semver: %q", s)
	}

	majorStr := leadingDigits(parts[0])
	minorStr := leadingDigits(parts[1])
	patchStr := leadingDigits(parts[2])

	if majorStr == "" || minorStr == "" || patchStr == "" {
		return version{}, fmt.Errorf("invalid semver: %q", s)
	}

	major, err := strconv.Atoi(majorStr)
	if err != nil {
		return version{}, fmt.Errorf("invalid major version %q: %w", majorStr, err)
	}

	minor, err := strconv.Atoi(minorStr)
	if err != nil {
		return version{}, fmt.Errorf("invalid minor version %q: %w", minorStr, err)
	}

	patch, err := strconv.Atoi(patchStr)
	if err != nil {
		return version{}, fmt.Errorf("invalid patch version %q: %w", patchStr, err)
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
func findAsset(release *release, name string) *asset {
	for i := range release.Assets {
		if release.Assets[i].Name == name {
			return &release.Assets[i]
		}
	}
	return nil
}

// findChecksumAsset searches release assets for a checksums file matching the
// given version.
func findChecksumAsset(release *release, version string) *asset {
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

// downloadAndVerify downloads the binary and checksums assets for a release,
// verifies the binary against the checksums, and replaces the running
// executable. releaseTag is used to find the checksums file in the release.
func downloadAndVerify(ctx context.Context, release *release, releaseTag, token string) (string, error) {
	name := assetName()
	asset := findAsset(release, name)
	if asset == nil {
		return "", fmt.Errorf("no asset found for %s in release %s", name, releaseTag)
	}

	// The checksums file uses the version without a leading "v".
	strippedTag := strings.TrimPrefix(releaseTag, "v")
	strippedTag = strings.TrimPrefix(strippedTag, "V")
	checksumAsset := findChecksumAsset(release, strippedTag)
	if checksumAsset == nil {
		return "", fmt.Errorf("no checksums file found for version %s", strippedTag)
	}

	// Download the binary.
	binaryData, err := downloadAsset(ctx, asset.DownloadURL, token)
	if err != nil {
		return "", fmt.Errorf("download asset: %w", err)
	}

	// Download checksums.
	checksumData, err := downloadChecksums(ctx, checksumAsset.DownloadURL, token)
	if err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}

	// Parse checksums.
	checksums, err := parseChecksums(checksumData)
	if err != nil {
		return "", fmt.Errorf("parse checksums: %w", err)
	}

	// Verify the binary.
	expectedHex, ok := checksums[name]
	if !ok {
		return "", fmt.Errorf("no checksum found for %s in checksums file", name)
	}

	if err := verifyChecksum(binaryData, expectedHex); err != nil {
		return "", fmt.Errorf("verify binary: %w", err)
	}

	// Get the running executable path.
	exePath, err := osExecutable()
	if err != nil {
		return "", fmt.Errorf("get executable path: %w", err)
	}

	// Resolve symlinks to get the real path.
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}

	// Replace the binary.
	if err := replaceBinary(exePath, binaryData); err != nil {
		return "", fmt.Errorf("replace binary: %w", err)
	}

	return releaseTag, nil
}

// isDevVersion reports whether v is a dev build version string that cannot be
// semver-compared: the literal "dev" or any version prefixed with "dev-".
func isDevVersion(v string) bool {
	return v == "dev" || strings.HasPrefix(v, "dev-")
}

// Channel checks for an update on the specified release channel and
// replaces the running binary if a newer version is available.
//
// Channel "stable" fetches the latest stable release and compares semver.
// Channel "dev" fetches the release tagged "dev" and skips version comparison.
//
// currentVersion should be the current semver string (with or without "v"
// prefix) for stable updates. A dev build version ("dev" or "dev-<sha>") is
// always treated as older than the latest stable release, allowing a dev build
// to update to a stable one. owner and repo identify the GitHub repository.
// If token is non-empty, it is used as a Bearer token for GitHub API requests.
//
// On success, the release tag name is returned. For stable updates, if the
// current version is already up to date, ErrUpToDate is returned alongside the
// latest tag.
func Channel(ctx context.Context, currentVersion, owner, repo, token, channel string) (string, error) {
	switch channel {
	case "dev":
		release, err := fetchReleaseByTag(ctx, owner, repo, "dev", token)
		if err != nil {
			return "", fmt.Errorf("fetch dev release: %w", err)
		}
		return downloadAndVerify(ctx, release, release.TagName, token)

	default:
		// Fetch the latest release.
		release, err := fetchLatestRelease(ctx, owner, repo, token)
		if err != nil {
			return "", fmt.Errorf("fetch latest release: %w", err)
		}

		// Compare versions.
		latestTag := release.TagName
		latestVer, err := parseVersion(latestTag)
		if err != nil {
			return "", fmt.Errorf("parse latest version %q: %w", latestTag, err)
		}

		// A dev build is never semver-comparable; always treat it as older
		// than the latest stable release so dev builds can update to stable.
		if isDevVersion(currentVersion) {
			return downloadAndVerify(ctx, release, latestTag, token)
		}

		currVer, err := parseVersion(currentVersion)
		if err != nil {
			return "", fmt.Errorf("parse current version %q: %w", currentVersion, err)
		}

		if !currVer.isOlderThan(latestVer) {
			return latestTag, ErrUpToDate
		}

		return downloadAndVerify(ctx, release, latestTag, token)
	}
}
