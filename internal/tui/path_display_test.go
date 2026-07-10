package tui

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestShortenAttachedImagePath(t *testing.T) {
	testHomeDir := "/home/testuser"
	workingDir := filepath.Join(testHomeDir, "project")
	homeDir := testHomeDir

	tests := []struct {
		name       string
		filePath   string
		workingDir string
		homeDir    string
		maxWidth   int
		wantPrefix string // Check that result starts with this prefix
	}{
		{
			name:       "workspace-relative path",
			filePath:   filepath.Join(workingDir, ".steiner", "tmp", "images", "test.png"),
			workingDir: workingDir,
			homeDir:    homeDir,
			maxWidth:   100,
			wantPrefix: ".steiner",
		},
		{
			name:       "home-relative fallback",
			filePath:   filepath.Join(homeDir, "other", "place", "image.png"),
			workingDir: "",
			homeDir:    homeDir,
			maxWidth:   100,
			wantPrefix: "~/",
		},
		{
			name:       "absolute path with no shortening",
			filePath:   "/var/tmp/image.png",
			workingDir: workingDir,
			homeDir:    homeDir,
			maxWidth:   100,
			wantPrefix: "/var/tmp",
		},
		{
			name:       "long path gets ellipsis",
			filePath:   filepath.Join(workingDir, ".steiner", "tmp", "images", "very_long_filename_that_exceeds_width.png"),
			workingDir: workingDir,
			homeDir:    homeDir,
			maxWidth:   20,
			wantPrefix: ".steiner",
		},
		{
			name:       "empty working dir falls back to home-relative",
			filePath:   filepath.Join(homeDir, "photos", "image.png"),
			workingDir: "",
			homeDir:    homeDir,
			maxWidth:   100,
			wantPrefix: "~/",
		},
		{
			name:       "relative working dir returns absolute path",
			filePath:   "/absolute/path/image.png",
			workingDir: "relative/path",
			homeDir:    homeDir,
			maxWidth:   100,
			wantPrefix: "/absolute/path",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shortenAttachedImagePath(tc.filePath, tc.workingDir, tc.homeDir, tc.maxWidth)
			if !strings.HasPrefix(got, tc.wantPrefix) {
				t.Errorf("shortenAttachedImagePath = %q, want prefix %q", got, tc.wantPrefix)
			}
		})
	}
}

func TestShortenAttachedImagePathWithRelativeCheck(t *testing.T) {
	workingDir := "/home/user/project"
	homeDir := "/home/user"
	filePath := "/home/user/project/.steiner/tmp/images/test.png"

	got := shortenAttachedImagePath(filePath, workingDir, homeDir, 100)
	want := ".steiner/tmp/images/test.png"
	if got != want {
		t.Errorf("shortenAttachedImagePath = %q, want %q", got, want)
	}
}

func TestShortenAttachedImagePathEllipsis(t *testing.T) {
	workingDir := "/home/user/project"
	homeDir := "/home/user"
	// Create a long path that will be shortened
	filePath := "/home/user/project/.steiner/tmp/images/very_long_name_that_exceeds_limit.png"

	got := shortenAttachedImagePath(filePath, workingDir, homeDir, 15)
	// Should contain middle-ellipsis
	if !strings.Contains(got, "…") {
		t.Errorf("shortenAttachedImagePath = %q, expected ellipsis for long path", got)
	}
}

func TestShortenAttachedImagePathHomeRelative(t *testing.T) {
	homeDir := "/home/user"
	filePath := "/home/user/photos/image.png"

	got := shortenAttachedImagePath(filePath, "", homeDir, 100)
	want := "~/photos/image.png"
	if got != want {
		t.Errorf("shortenAttachedImagePath = %q, want %q", got, want)
	}
}
