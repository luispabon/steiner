package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input   string
		want    version
		wantErr bool
	}{
		{"v1.2.3", version{1, 2, 3}, false},
		{"V1.2.3", version{1, 2, 3}, false},
		{"1.2.3", version{1, 2, 3}, false},
		{"v0.0.1", version{0, 0, 1}, false},
		{"v10.20.30", version{10, 20, 30}, false},
		{"", version{}, true},
		{"abc", version{}, true},
		{"v1.2", version{}, true},
		{"v1.2.x", version{}, true},
		{"v1.2.3.4", version{}, true},
	}

	for _, tt := range tests {
		got, err := parseVersion(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseVersion(%q): want error, got %+v", tt.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseVersion(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseVersion(%q) = %+v, want %+v", tt.input, got, tt.want)
		}
	}
}

func TestVersionIsOlderThan(t *testing.T) {
	tests := []struct {
		a, b version
		want bool
	}{
		{version{1, 0, 0}, version{1, 0, 1}, true},
		{version{1, 0, 0}, version{1, 1, 0}, true},
		{version{1, 0, 0}, version{2, 0, 0}, true},
		{version{1, 0, 0}, version{1, 0, 0}, false},
		{version{2, 0, 0}, version{1, 0, 0}, false},
		{version{1, 1, 0}, version{1, 0, 0}, false},
		{version{1, 0, 1}, version{1, 0, 0}, false},
	}

	for _, tt := range tests {
		got := tt.a.isOlderThan(tt.b)
		if got != tt.want {
			t.Errorf("%+v.isOlderThan(%+v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	name := assetName()
	if name == "" {
		t.Fatal("assetName() returned empty")
	}
	if len(name) < 9 || name[:8] != "steiner-" {
		t.Errorf("assetName() = %q, want prefix \"steiner-\"", name)
	}
	if runtime.GOOS == "windows" {
		if len(name) < 5 || name[len(name)-4:] != ".exe" {
			t.Errorf("assetName() on windows = %q, want .exe suffix", name)
		}
	}
}

func TestFindAsset(t *testing.T) {
	release := &Release{
		TagName: "v1.0.0",
		Assets: []Asset{
			{Name: "steiner-linux-amd64", DownloadURL: "https://example.com/linux"},
			{Name: "steiner-darwin-amd64", DownloadURL: "https://example.com/darwin"},
		},
	}

	asset := findAsset(release, "steiner-linux-amd64")
	if asset == nil {
		t.Fatal("findAsset: expected to find asset")
	}
	if asset.DownloadURL != "https://example.com/linux" {
		t.Errorf("findAsset: got URL %q, want %q", asset.DownloadURL, "https://example.com/linux")
	}

	asset = findAsset(release, "nonexistent")
	if asset != nil {
		t.Errorf("findAsset: expected nil for nonexistent, got %+v", asset)
	}
}

func TestFindChecksumAsset(t *testing.T) {
	release := &Release{
		TagName: "v1.0.0",
		Assets: []Asset{
			{Name: "steiner_1.0.0_checksums.txt", DownloadURL: "https://example.com/checksums"},
		},
	}

	asset := findChecksumAsset(release, "1.0.0")
	if asset == nil {
		t.Fatal("findChecksumAsset: expected to find asset")
	}
	if asset.DownloadURL != "https://example.com/checksums" {
		t.Errorf("findChecksumAsset: got URL %q, want %q", asset.DownloadURL, "https://example.com/checksums")
	}

	asset = findChecksumAsset(release, "2.0.0")
	if asset != nil {
		t.Errorf("findChecksumAsset: expected nil for nonexistent, got %+v", asset)
	}
}

func TestParseChecksums(t *testing.T) {
	data := []byte("a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b  steiner-linux-amd64\n" +
		"b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c  steiner-darwin-amd64\n" +
		"\n" +
		"c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d  steiner-darwin-arm64\n")

	checksums, err := parseChecksums(data)
	if err != nil {
		t.Fatalf("parseChecksums: %v", err)
	}

	if len(checksums) != 3 {
		t.Errorf("parseChecksums: got %d entries, want 3", len(checksums))
	}

	cases := []struct {
		file     string
		checksum string
	}{
		{"steiner-linux-amd64", "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b"},
		{"steiner-darwin-amd64", "b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c"},
		{"steiner-darwin-arm64", "c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d"},
	}

	for _, tc := range cases {
		got, ok := checksums[tc.file]
		if !ok {
			t.Errorf("parseChecksums: missing entry for %q", tc.file)
			continue
		}
		if got != tc.checksum {
			t.Errorf("parseChecksums[%q] = %q, want %q", tc.file, got, tc.checksum)
		}
	}

	// Malformed lines are silently skipped.
	malformed := []byte("invalid")
	checksums, err = parseChecksums(malformed)
	if err != nil {
		t.Fatalf("parseChecksums(malformed): %v", err)
	}
	if len(checksums) != 0 {
		t.Errorf("parseChecksums(malformed): got %d entries, want 0", len(checksums))
	}
}

func TestVerifyChecksum(t *testing.T) {
	data := []byte("test binary content")
	sum := sha256.Sum256(data)
	hexSum := hex.EncodeToString(sum[:])

	if err := verifyChecksum(data, hexSum); err != nil {
		t.Errorf("verifyChecksum: unexpected error: %v", err)
	}

	if err := verifyChecksum(data, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Error("verifyChecksum: expected error for wrong checksum, got nil")
	}

	if err := verifyChecksum(data, "nothex"); err == nil {
		t.Error("verifyChecksum: expected error for invalid hex, got nil")
	}
}

func TestReplaceBinary(t *testing.T) {
	testDir := t.TempDir()
	exePath := filepath.Join(testDir, "steiner")
	oldPath := exePath + ".old"
	tmpPath := exePath + ".tmp"

	initialContent := []byte("original binary")
	if err := os.WriteFile(exePath, initialContent, 0o755); err != nil {
		t.Fatalf("write initial exe: %v", err)
	}

	newContent := []byte("updated binary")
	if err := replaceBinary(exePath, newContent); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}

	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("read updated exe: %v", err)
	}
	if string(got) != string(newContent) {
		t.Errorf("replaceBinary: got %q, want %q", got, newContent)
	}

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("replaceBinary: .old file should be removed, stat err: %v", err)
	}

	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("replaceBinary: .tmp file should be removed, stat err: %v", err)
	}
}

func TestReplaceBinary_Rollback(t *testing.T) {
	testDir := t.TempDir()
	roDir := filepath.Join(testDir, "readonly")
	roPath := filepath.Join(roDir, "steiner")

	if err := os.MkdirAll(roDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	initialContent := []byte("original binary")
	if err := os.WriteFile(roPath, initialContent, 0o755); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	// Make directory read-only so writing .tmp will fail after rename.
	if err := os.Chmod(roDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	err := replaceBinary(roPath, []byte("new content"))
	if err == nil {
		// Restore permissions before failing.
		if chErr := os.Chmod(roDir, 0o755); chErr != nil {
			t.Errorf("chmod restore: %v", chErr)
		}
		t.Fatal("replaceBinary: expected error for read-only directory, got nil")
	}

	// Restore permissions to verify rollback.
	if err := os.Chmod(roDir, 0o755); err != nil {
		t.Fatalf("chmod restore: %v", err)
	}

	got, err := os.ReadFile(roPath)
	if err != nil {
		t.Fatalf("read after rollback: %v", err)
	}
	if string(got) != string(initialContent) {
		t.Errorf("replaceBinary rollback: got %q, want %q", got, initialContent)
	}

	if _, err := os.Stat(roPath + ".old"); !os.IsNotExist(err) {
		t.Errorf("replaceBinary: .old should be gone after rollback, stat err: %v", err)
	}
}

func TestFetchLatestReleaseDirect(t *testing.T) {
	data, err := os.ReadFile("testdata/release.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var release Release
	if err := json.Unmarshal(data, &release); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	if release.TagName != "v1.0.0" {
		t.Errorf("TagName = %q, want %q", release.TagName, "v1.0.0")
	}

	if len(release.Assets) != 5 {
		t.Fatalf("expected 5 assets, got %d", len(release.Assets))
	}

	expectedAssets := map[string]string{
		"steiner-linux-amd64":         "https://github.com/luispabon/steiner/releases/download/v1.0.0/steiner-linux-amd64",
		"steiner-darwin-amd64":        "https://github.com/luispabon/steiner/releases/download/v1.0.0/steiner-darwin-amd64",
		"steiner-darwin-arm64":        "https://github.com/luispabon/steiner/releases/download/v1.0.0/steiner-darwin-arm64",
		"steiner-windows-amd64.exe":   "https://github.com/luispabon/steiner/releases/download/v1.0.0/steiner-windows-amd64.exe",
		"steiner_1.0.0_checksums.txt": "https://github.com/luispabon/steiner/releases/download/v1.0.0/steiner_1.0.0_checksums.txt",
	}

	for _, asset := range release.Assets {
		expectedURL, ok := expectedAssets[asset.Name]
		if !ok {
			t.Errorf("unexpected asset: %q", asset.Name)
			continue
		}
		if asset.DownloadURL != expectedURL {
			t.Errorf("asset[%q].DownloadURL = %q, want %q", asset.Name, asset.DownloadURL, expectedURL)
		}
	}
}

func TestDownloadURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer token123" {
			_, _ = w.Write([]byte("authenticated content"))
		} else {
			_, _ = w.Write([]byte("public content"))
		}
	}))
	defer server.Close()

	data, err := downloadURL(context.Background(), server.URL, "")
	if err != nil {
		t.Fatalf("downloadURL without token: %v", err)
	}
	if string(data) != "public content" {
		t.Errorf("downloadURL without token: got %q, want %q", data, "public content")
	}

	data, err = downloadURL(context.Background(), server.URL, "token123")
	if err != nil {
		t.Fatalf("downloadURL with token: %v", err)
	}
	if string(data) != "authenticated content" {
		t.Errorf("downloadURL with token: got %q, want %q", data, "authenticated content")
	}
}

func TestDownloadURL_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := downloadURL(context.Background(), server.URL, "")
	if err == nil {
		t.Fatal("downloadURL: expected error for 404, got nil")
	}
}

func TestChecksumsFileName(t *testing.T) {
	got := checksumsFileName("1.0.0")
	want := "steiner_1.0.0_checksums.txt"
	if got != want {
		t.Errorf("checksumsFileName(\"1.0.0\") = %q, want %q", got, want)
	}
}
