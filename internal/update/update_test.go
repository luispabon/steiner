package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
		{"v1.2.3.4", version{1, 2, 3}, false},
		{"1.2.3.4.5", version{1, 2, 3}, false},
		{"0.0.4-8-g8bd663f", version{0, 0, 4}, false},
		{"v1.2.3-alpha", version{1, 2, 3}, false},
		{"1.2.3+build", version{1, 2, 3}, false},
		{"v1.2.3-alpha+build", version{1, 2, 3}, false},
		{"1.2.3.4.5", version{1, 2, 3}, false},
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
	release := &release{
		TagName: "v1.0.0",
		Assets: []asset{
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
	release := &release{
		TagName: "v1.0.0",
		Assets: []asset{
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
	exePath := filepath.Join(testDir, "steiner")
	oldPath := exePath + ".old"
	tmpPath := exePath + ".tmp"

	initialContent := []byte("original binary")
	if err := os.WriteFile(exePath, initialContent, 0o755); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	// Pre-create tmpPath as a directory so the initial rename to .old
	// succeeds, but the subsequent WriteFile to .tmp fails with EISDIR,
	// forcing control into the rollback defer.
	if err := os.Mkdir(tmpPath, 0o755); err != nil {
		t.Fatalf("mkdir tmpPath: %v", err)
	}

	err := replaceBinary(exePath, []byte("new content"))
	if err == nil {
		t.Fatal("replaceBinary: expected error writing temp binary, got nil")
	}
	if !strings.Contains(err.Error(), "write temp binary") {
		t.Fatalf("replaceBinary: error = %v, want write temp binary error (proves rename to .old succeeded first)", err)
	}

	if _, statErr := os.Stat(oldPath); !os.IsNotExist(statErr) {
		t.Errorf("replaceBinary: .old should be gone after rollback, stat err: %v", statErr)
	}

	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("read after rollback: %v", err)
	}
	if string(got) != string(initialContent) {
		t.Errorf("replaceBinary rollback: got %q, want %q", got, initialContent)
	}
}

func TestFetchLatestReleaseDirect(t *testing.T) {
	data, err := os.ReadFile("testdata/release.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var release release
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

// saveHTTPClient returns a function that restores httpClient to its original value.
func saveHTTPClient() func() {
	old := httpClient
	return func() { httpClient = old }
}

// saveOSExecutable returns a function that restores osExecutable to its original value.
func saveOSExecutable() func() {
	old := osExecutable
	return func() { osExecutable = old }
}

// redirectTransport redirects all HTTP requests to a base URL.
type redirectTransport struct {
	base *url.URL
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = t.base.Scheme
	req.URL.Host = t.base.Host
	return http.DefaultTransport.RoundTrip(req)
}

// newTestClient creates an HTTP client that routes all requests to the given server URL.
func newTestClient(serverURL string) *http.Client {
	baseURL, err := url.Parse(serverURL)
	if err != nil {
		panic(err)
	}
	return &http.Client{
		Transport: &redirectTransport{base: baseURL},
	}
}

func TestUpdate_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "steiner")
	if err := os.WriteFile(exePath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	defer saveOSExecutable()()
	osExecutable = func() (string, error) {
		return exePath, nil
	}

	an := assetName()
	binaryContent := []byte("new binary content")
	hash := sha256.Sum256(binaryContent)
	hexHash := hex.EncodeToString(hash[:])
	checksumsContent := hexHash + "  " + an + "\n"

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			rel := release{
				TagName: "v2.0.0",
				Assets: []asset{
					{Name: an, DownloadURL: server.URL + "/asset"},
					{Name: "steiner_2.0.0_checksums.txt", DownloadURL: server.URL + "/checksums"},
				},
			}
			if err := json.NewEncoder(w).Encode(rel); err != nil {
				t.Errorf("encode release: %v", err)
			}
		case "/asset":
			_, _ = w.Write(binaryContent)
		case "/checksums":
			_, _ = w.Write([]byte(checksumsContent))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := Channel(context.Background(), "v1.0.0", "owner", "repo", "", "stable", "")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(binaryContent) {
		t.Errorf("Update: executable content = %q, want %q", got, binaryContent)
	}
}

func TestUpdate_AlreadyUpToDate(t *testing.T) {
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "steiner")
	if err := os.WriteFile(exePath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	defer saveOSExecutable()()
	osExecutable = func() (string, error) {
		return exePath, nil
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rel := release{TagName: "v1.0.0"}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	latestVer, err := Channel(context.Background(), "v1.0.0", "owner", "repo", "", "stable", "")
	if !errors.Is(err, ErrUpToDate) {
		t.Fatalf("Update: want ErrUpToDate, got %v", err)
	}
	if latestVer != "v1.0.0" {
		t.Errorf("Update: latestVer = %q, want %q", latestVer, "v1.0.0")
	}
}

func TestUpdate_InvalidCurrentVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rel := release{TagName: "v2.0.0"}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := Channel(context.Background(), "invalid", "owner", "repo", "", "stable", "")
	if err == nil {
		t.Fatal("Update: expected error for invalid version, got nil")
	}
	if !strings.Contains(err.Error(), "parse current version") {
		t.Errorf("Update: error = %v, want parse current version error", err)
	}
}

func TestUpdate_InvalidLatestVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rel := release{TagName: "invalid"}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := Channel(context.Background(), "v1.0.0", "owner", "repo", "", "stable", "")
	if err == nil {
		t.Fatal("Update: expected error for invalid version, got nil")
	}
	if !strings.Contains(err.Error(), "parse latest version") {
		t.Errorf("Update: error = %v, want parse latest version error", err)
	}
}

func TestUpdate_GitHubAPINon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := Channel(context.Background(), "v1.0.0", "owner", "repo", "", "stable", "")
	if err == nil {
		t.Fatal("Update: expected error for 500, got nil")
	}
	if !strings.Contains(err.Error(), "GitHub API returned 500") {
		t.Errorf("Update: error = %v, want GitHub API 500 error", err)
	}
}

func TestUpdate_ReleaseJSONMissingAsset(t *testing.T) {
	an := assetName()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rel := release{
			TagName: "v2.0.0",
			Assets: []asset{
				{Name: "nonexistent-asset", DownloadURL: server.URL + "/asset"},
				{Name: "steiner_2.0.0_checksums.txt", DownloadURL: server.URL + "/checksums"},
			},
		}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := Channel(context.Background(), "v1.0.0", "owner", "repo", "", "stable", "")
	if err == nil {
		t.Fatal("Update: expected error for missing asset, got nil")
	}
	if !strings.Contains(err.Error(), "no asset found for "+an) {
		t.Errorf("Update: error = %v, want missing asset error", err)
	}
}

func TestUpdate_ReleaseJSONMissingChecksumsAsset(t *testing.T) {
	an := assetName()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rel := release{
			TagName: "v2.0.0",
			Assets: []asset{
				{Name: an, DownloadURL: server.URL + "/asset"},
			},
		}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := Channel(context.Background(), "v1.0.0", "owner", "repo", "", "stable", "")
	if err == nil {
		t.Fatal("Update: expected error for missing checksums asset, got nil")
	}
	if !strings.Contains(err.Error(), "no checksums file found") {
		t.Errorf("Update: error = %v, want missing checksums asset error", err)
	}
}

func TestUpdate_AssetDownloadFails(t *testing.T) {
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "steiner")
	if err := os.WriteFile(exePath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	defer saveOSExecutable()()
	osExecutable = func() (string, error) {
		return exePath, nil
	}

	an := assetName()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			rel := release{
				TagName: "v2.0.0",
				Assets: []asset{
					{Name: an, DownloadURL: server.URL + "/asset"},
					{Name: "steiner_2.0.0_checksums.txt", DownloadURL: server.URL + "/checksums"},
				},
			}
			_ = json.NewEncoder(w).Encode(rel)
		case "/asset":
			w.WriteHeader(http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := Channel(context.Background(), "v1.0.0", "owner", "repo", "", "stable", "")
	if err == nil {
		t.Fatal("Update: expected error for asset download failure, got nil")
	}
	if !strings.Contains(err.Error(), "download asset") {
		t.Errorf("Update: error = %v, want download asset error", err)
	}
}

func TestUpdate_ChecksumDownloadFails(t *testing.T) {
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "steiner")
	if err := os.WriteFile(exePath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	defer saveOSExecutable()()
	osExecutable = func() (string, error) {
		return exePath, nil
	}

	an := assetName()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			rel := release{
				TagName: "v2.0.0",
				Assets: []asset{
					{Name: an, DownloadURL: server.URL + "/asset"},
					{Name: "steiner_2.0.0_checksums.txt", DownloadURL: server.URL + "/checksums"},
				},
			}
			_ = json.NewEncoder(w).Encode(rel)
		case "/asset":
			_, _ = w.Write([]byte("binary content"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := Channel(context.Background(), "v1.0.0", "owner", "repo", "", "stable", "")
	if err == nil {
		t.Fatal("Update: expected error for checksum download failure, got nil")
	}
	if !strings.Contains(err.Error(), "download checksums") {
		t.Errorf("Update: error = %v, want download checksums error", err)
	}
}

func TestUpdate_ChecksumMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "steiner")
	if err := os.WriteFile(exePath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	defer saveOSExecutable()()
	osExecutable = func() (string, error) {
		return exePath, nil
	}

	an := assetName()
	binaryContent := []byte("new binary content")
	// Deliberately wrong checksum
	checksumsContent := "0000000000000000000000000000000000000000000000000000000000000000  " + an + "\n"

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			rel := release{
				TagName: "v2.0.0",
				Assets: []asset{
					{Name: an, DownloadURL: server.URL + "/asset"},
					{Name: "steiner_2.0.0_checksums.txt", DownloadURL: server.URL + "/checksums"},
				},
			}
			_ = json.NewEncoder(w).Encode(rel)
		case "/asset":
			_, _ = w.Write(binaryContent)
		case "/checksums":
			_, _ = w.Write([]byte(checksumsContent))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := Channel(context.Background(), "v1.0.0", "owner", "repo", "", "stable", "")
	if err == nil {
		t.Fatal("Update: expected error for checksum mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "verify binary") {
		t.Errorf("Update: error = %v, want verify binary error", err)
	}
}

func TestUpdate_MissingChecksumEntry(t *testing.T) {
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "steiner")
	if err := os.WriteFile(exePath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	defer saveOSExecutable()()
	osExecutable = func() (string, error) {
		return exePath, nil
	}

	an := assetName()
	binaryContent := []byte("new binary content")
	hash := sha256.Sum256(binaryContent)
	hexHash := hex.EncodeToString(hash[:])
	// Checksums file missing the asset entry - includes a different asset
	checksumsContent := hexHash + "  some-other-asset\n"

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			rel := release{
				TagName: "v2.0.0",
				Assets: []asset{
					{Name: an, DownloadURL: server.URL + "/asset"},
					{Name: "steiner_2.0.0_checksums.txt", DownloadURL: server.URL + "/checksums"},
				},
			}
			_ = json.NewEncoder(w).Encode(rel)
		case "/asset":
			_, _ = w.Write(binaryContent)
		case "/checksums":
			_, _ = w.Write([]byte(checksumsContent))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := Channel(context.Background(), "v1.0.0", "owner", "repo", "", "stable", "")
	if err == nil {
		t.Fatal("Update: expected error for missing checksum entry, got nil")
	}
	if !strings.Contains(err.Error(), "no checksum found for "+an) {
		t.Errorf("Update: error = %v, want missing checksum entry error", err)
	}
}

func TestUpdate_OsExecutableFails(t *testing.T) {
	an := assetName()
	binaryContent := []byte("new binary content")
	hash := sha256.Sum256(binaryContent)
	hexHash := hex.EncodeToString(hash[:])
	checksumsContent := hexHash + "  " + an + "\n"

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			rel := release{
				TagName: "v2.0.0",
				Assets: []asset{
					{Name: an, DownloadURL: server.URL + "/asset"},
					{Name: "steiner_2.0.0_checksums.txt", DownloadURL: server.URL + "/checksums"},
				},
			}
			_ = json.NewEncoder(w).Encode(rel)
		case "/asset":
			_, _ = w.Write(binaryContent)
		case "/checksums":
			_, _ = w.Write([]byte(checksumsContent))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	defer saveOSExecutable()()
	osExecutable = func() (string, error) {
		return "", fmt.Errorf("executable error")
	}

	_, err := Channel(context.Background(), "v1.0.0", "owner", "repo", "", "stable", "")
	if err == nil {
		t.Fatal("Update: expected error for os.Executable failure, got nil")
	}
	if !strings.Contains(err.Error(), "get executable path") {
		t.Errorf("Update: error = %v, want get executable path error", err)
	}
}

func TestDownloadAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("asset content"))
	}))
	defer server.Close()

	data, err := downloadAsset(context.Background(), server.URL, "")
	if err != nil {
		t.Fatalf("downloadAsset: %v", err)
	}
	if string(data) != "asset content" {
		t.Errorf("downloadAsset: got %q, want %q", data, "asset content")
	}
}

func TestDownloadChecksums(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("checksums content"))
	}))
	defer server.Close()

	data, err := downloadChecksums(context.Background(), server.URL, "")
	if err != nil {
		t.Fatalf("downloadChecksums: %v", err)
	}
	if string(data) != "checksums content" {
		t.Errorf("downloadChecksums: got %q, want %q", data, "checksums content")
	}
}

func TestDownloadURL_Non200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()

	_, err := downloadURL(context.Background(), server.URL, "")
	if err == nil {
		t.Fatal("downloadURL: expected error for 418, got nil")
	}
	if !strings.Contains(err.Error(), "download returned 418") {
		t.Errorf("downloadURL: error = %v, want download returned 418", err)
	}
}

func TestChannel_Dev_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "steiner")
	if err := os.WriteFile(exePath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	defer saveOSExecutable()()
	osExecutable = func() (string, error) {
		return exePath, nil
	}

	an := assetName()
	binaryContent := []byte("dev binary content")
	hash := sha256.Sum256(binaryContent)
	hexHash := hex.EncodeToString(hash[:])
	// Dev checksums file uses "dev" as version.
	checksumsContent := hexHash + "  " + an + "\n"

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/tags/dev":
			w.Header().Set("Content-Type", "application/json")
			rel := release{
				TagName: "dev",
				Assets: []asset{
					{Name: an, DownloadURL: server.URL + "/asset"},
					{Name: "steiner_dev_checksums.txt", DownloadURL: server.URL + "/checksums"},
				},
			}
			if err := json.NewEncoder(w).Encode(rel); err != nil {
				t.Errorf("encode release: %v", err)
			}
		case "/asset":
			_, _ = w.Write(binaryContent)
		case "/checksums":
			_, _ = w.Write([]byte(checksumsContent))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	tag, err := Channel(context.Background(), "v1.0.0", "owner", "repo", "", "dev", "")
	if err != nil {
		t.Fatalf("Channel(dev): %v", err)
	}
	if tag != "dev" {
		t.Errorf("Channel(dev) tag = %q, want %q", tag, "dev")
	}

	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(binaryContent) {
		t.Errorf("Channel(dev): executable content = %q, want %q", got, binaryContent)
	}
}

func TestChannel_Dev_Non200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := Channel(context.Background(), "v1.0.0", "owner", "repo", "", "dev", "")
	if err == nil {
		t.Fatal("Channel(dev): expected error for 404, got nil")
	}
	if !strings.Contains(err.Error(), "fetch dev release") {
		t.Errorf("Channel(dev): error = %v, want fetch dev release error", err)
	}
}

func TestChannel_StableIsDefault(t *testing.T) {
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "steiner")
	if err := os.WriteFile(exePath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	defer saveOSExecutable()()
	osExecutable = func() (string, error) {
		return exePath, nil
	}

	an := assetName()
	binaryContent := []byte("new binary content")
	hash := sha256.Sum256(binaryContent)
	hexHash := hex.EncodeToString(hash[:])
	checksumsContent := hexHash + "  " + an + "\n"

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			rel := release{
				TagName: "v2.0.0",
				Assets: []asset{
					{Name: an, DownloadURL: server.URL + "/asset"},
					{Name: "steiner_2.0.0_checksums.txt", DownloadURL: server.URL + "/checksums"},
				},
			}
			if err := json.NewEncoder(w).Encode(rel); err != nil {
				t.Errorf("encode release: %v", err)
			}
		case "/asset":
			_, _ = w.Write(binaryContent)
		case "/checksums":
			_, _ = w.Write([]byte(checksumsContent))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := Channel(context.Background(), "v1.0.0", "owner", "repo", "", "stable", "")
	if err != nil {
		t.Fatalf("Channel(stable): %v", err)
	}

	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(binaryContent) {
		t.Errorf("Channel(stable): executable content = %q, want %q", got, binaryContent)
	}
}

func TestChannel_StableFromDevBuild(t *testing.T) {
	// A dev build current version should be treated as older than any stable
	// release so dev builds can update to stable.
	tests := []struct {
		name           string
		currentVersion string
	}{
		{"literal dev", "dev"},
		{"dev with sha", "dev-abc1234"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			exePath := filepath.Join(tmpDir, "steiner")
			if err := os.WriteFile(exePath, []byte("old binary"), 0o755); err != nil {
				t.Fatal(err)
			}

			defer saveOSExecutable()()
			osExecutable = func() (string, error) {
				return exePath, nil
			}

			an := assetName()
			binaryContent := []byte("stable binary content")
			hash := sha256.Sum256(binaryContent)
			hexHash := hex.EncodeToString(hash[:])
			checksumsContent := hexHash + "  " + an + "\n"

			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/owner/repo/releases/latest":
					w.Header().Set("Content-Type", "application/json")
					rel := release{
						TagName: "v0.0.22",
						Assets: []asset{
							{Name: an, DownloadURL: server.URL + "/asset"},
							{Name: "steiner_0.0.22_checksums.txt", DownloadURL: server.URL + "/checksums"},
						},
					}
					if err := json.NewEncoder(w).Encode(rel); err != nil {
						t.Errorf("encode release: %v", err)
					}
				case "/asset":
					_, _ = w.Write(binaryContent)
				case "/checksums":
					_, _ = w.Write([]byte(checksumsContent))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			defer saveHTTPClient()()
			httpClient = newTestClient(server.URL)

			tag, err := Channel(context.Background(), tt.currentVersion, "owner", "repo", "", "stable", "")
			if err != nil {
				t.Fatalf("Channel(stable, %q): %v", tt.currentVersion, err)
			}
			if tag != "v0.0.22" {
				t.Errorf("Channel(stable, %q) tag = %q, want %q", tt.currentVersion, tag, "v0.0.22")
			}

			got, err := os.ReadFile(exePath)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(binaryContent) {
				t.Errorf("Channel(stable, %q): executable content = %q, want %q", tt.currentVersion, got, binaryContent)
			}
		})
	}
}

func TestChannel_UnknownChannelIsStable(t *testing.T) {
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "steiner")
	if err := os.WriteFile(exePath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	defer saveOSExecutable()()
	osExecutable = func() (string, error) {
		return exePath, nil
	}

	an := assetName()
	binaryContent := []byte("new binary content")
	hash := sha256.Sum256(binaryContent)
	hexHash := hex.EncodeToString(hash[:])
	checksumsContent := hexHash + "  " + an + "\n"

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			rel := release{
				TagName: "v2.0.0",
				Assets: []asset{
					{Name: an, DownloadURL: server.URL + "/asset"},
					{Name: "steiner_2.0.0_checksums.txt", DownloadURL: server.URL + "/checksums"},
				},
			}
			if err := json.NewEncoder(w).Encode(rel); err != nil {
				t.Errorf("encode release: %v", err)
			}
		case "/asset":
			_, _ = w.Write(binaryContent)
		case "/checksums":
			_, _ = w.Write([]byte(checksumsContent))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := Channel(context.Background(), "v1.0.0", "owner", "repo", "", "unknown", "")
	if err != nil {
		t.Fatalf("Channel(unknown): %v", err)
	}

	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(binaryContent) {
		t.Errorf("Channel(unknown): executable content = %q, want %q", got, binaryContent)
	}
}

func TestChannel_Dev_MissingAsset(t *testing.T) {
	an := assetName()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rel := release{
			TagName: "dev",
			Assets: []asset{
				{Name: "nonexistent-asset", DownloadURL: server.URL + "/asset"},
				{Name: "steiner_dev_checksums.txt", DownloadURL: server.URL + "/checksums"},
			},
		}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := Channel(context.Background(), "dev", "owner", "repo", "", "dev", "")
	if err == nil {
		t.Fatal("Channel(dev): expected error for missing asset, got nil")
	}
	if !strings.Contains(err.Error(), "no asset found for "+an) {
		t.Errorf("Channel(dev): error = %v, want missing asset error", err)
	}
}

func TestNormalizeVersionTag(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"v1.0.0", "v1.0.0"},
		{"1.0.0", "v1.0.0"},
		{"dev", "dev"},
		{"", ""},
		{"V1.0.0", "V1.0.0"},
		{"dev-abc1234", "dev-abc1234"},
	}
	for _, tt := range tests {
		got := normalizeVersionTag(tt.input)
		if got != tt.want {
			t.Errorf("normalizeVersionTag(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCheck_StableUpdateAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rel := release{TagName: "v1.2.0"}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	latestVer, needsUpdate, err := Check(context.Background(), "v1.0.0", "owner", "repo", "", "stable", "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if latestVer != "v1.2.0" {
		t.Errorf("Check latestVer = %q, want %q", latestVer, "v1.2.0")
	}
	if !needsUpdate {
		t.Errorf("Check needsUpdate = false, want true")
	}
}

func TestCheck_StableUpToDate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rel := release{TagName: "v1.2.0"}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	latestVer, needsUpdate, err := Check(context.Background(), "v1.2.0", "owner", "repo", "", "stable", "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if latestVer != "v1.2.0" {
		t.Errorf("Check latestVer = %q, want %q", latestVer, "v1.2.0")
	}
	if needsUpdate {
		t.Errorf("Check needsUpdate = true, want false")
	}
}

func TestCheck_DevAlwaysNeedsUpdate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rel := release{TagName: "v1.2.0"}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	latestVer, needsUpdate, err := Check(context.Background(), "dev", "owner", "repo", "", "stable", "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if latestVer != "v1.2.0" {
		t.Errorf("Check latestVer = %q, want %q", latestVer, "v1.2.0")
	}
	if !needsUpdate {
		t.Errorf("Check needsUpdate = false, want true")
	}
}

func TestCheck_DevChannel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rel := release{TagName: "dev"}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	latestVer, needsUpdate, err := Check(context.Background(), "v1.0.0", "owner", "repo", "", "dev", "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if latestVer != "dev" {
		t.Errorf("Check latestVer = %q, want %q", latestVer, "dev")
	}
	if !needsUpdate {
		t.Errorf("Check needsUpdate = false, want true")
	}
}

func TestCheck_SpecificVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rel := release{TagName: "v1.0.0"}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	latestVer, needsUpdate, err := Check(context.Background(), "v1.2.0", "owner", "repo", "", "stable", "v1.0.0")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if latestVer != "v1.0.0" {
		t.Errorf("Check latestVer = %q, want %q", latestVer, "v1.0.0")
	}
	if needsUpdate {
		t.Errorf("Check needsUpdate = true, want false (current v1.2.0 >= v1.0.0)")
	}
}

func TestCheck_SpecificVersionWithoutV(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Should request tag "v1.0.0" (normalized)
		if strings.Contains(r.URL.Path, "/tags/v1.0.0") {
			w.Header().Set("Content-Type", "application/json")
			rel := release{TagName: "v1.0.0"}
			_ = json.NewEncoder(w).Encode(rel)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, _, err := Check(context.Background(), "v1.2.0", "owner", "repo", "", "stable", "1.0.0")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
}
