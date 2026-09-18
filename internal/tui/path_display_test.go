package tui

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestShortenAttachedImagePath(t *testing.T) {
	t.Parallel()
	testHomeDir := "/home/testuser"
	workingDir := filepath.Join(testHomeDir, "project")
	homeDir := testHomeDir

	tests := []struct {
		name         string
		filePath     string
		workingDir   string
		homeDir      string
		maxWidth     int
		want         string // Exact expected value
		wantEllipsis bool   // Whether ellipsis is expected when maxWidth is enforced
	}{
		{
			name:         "workspace-relative path",
			filePath:     filepath.Join(workingDir, ".steiner", "tmp", "images", "test.png"),
			workingDir:   workingDir,
			homeDir:      homeDir,
			maxWidth:     100,
			want:         ".steiner/tmp/images/test.png",
			wantEllipsis: false,
		},
		{
			name:         "home-relative fallback",
			filePath:     filepath.Join(homeDir, "other", "place", "image.png"),
			workingDir:   "",
			homeDir:      homeDir,
			maxWidth:     100,
			want:         "~/other/place/image.png",
			wantEllipsis: false,
		},
		{
			name:         "absolute path with no shortening",
			filePath:     "/var/tmp/image.png",
			workingDir:   workingDir,
			homeDir:      homeDir,
			maxWidth:     100,
			want:         "/var/tmp/image.png",
			wantEllipsis: false,
		},
		{
			name:         "long path gets ellipsis",
			filePath:     filepath.Join(workingDir, ".steiner", "tmp", "images", "very_long_filename_that_exceeds_width.png"),
			workingDir:   workingDir,
			homeDir:      homeDir,
			maxWidth:     20,
			want:         "", // Don't check exact value, just ellipsis
			wantEllipsis: true,
		},
		{
			name:         "empty working dir falls back to home-relative",
			filePath:     filepath.Join(homeDir, "photos", "image.png"),
			workingDir:   "",
			homeDir:      homeDir,
			maxWidth:     100,
			want:         "~/photos/image.png",
			wantEllipsis: false,
		},
		{
			name:         "relative working dir returns absolute path",
			filePath:     "/absolute/path/image.png",
			workingDir:   "relative/path",
			homeDir:      homeDir,
			maxWidth:     100,
			want:         "/absolute/path/image.png",
			wantEllipsis: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := shortenAttachedImagePath(tc.filePath, tc.workingDir, tc.homeDir, tc.maxWidth)
			if tc.wantEllipsis {
				if !strings.Contains(got, "…") {
					t.Errorf("shortenAttachedImagePath = %q, expected ellipsis for long path with maxWidth %d", got, tc.maxWidth)
				}
			} else {
				if got != tc.want {
					t.Errorf("shortenAttachedImagePath = %q, want %q", got, tc.want)
				}
			}
		})
	}
}

func TestShortenAttachedImagePathWithRelativeCheck(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	homeDir := "/home/user"
	filePath := "/home/user/photos/image.png"

	got := shortenAttachedImagePath(filePath, "", homeDir, 100)
	want := "~/photos/image.png"
	if got != want {
		t.Errorf("shortenAttachedImagePath = %q, want %q", got, want)
	}
}
