package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestRenderImagesAttachedSegment(t *testing.T) {
	b := &contentBuffer{
		styles: theme.BuildStyles(theme.AccentAmber),
	}

	tests := []struct {
		name   string
		rows   []imagesAttachedRowData
		width  int
		checks func(t *testing.T, output string)
	}{
		{
			name: "single image on one line",
			rows: []imagesAttachedRowData{
				{
					id:     "img-1",
					width:  800,
					height: 600,
					size:   "245KB",
					path:   ".steiner/tmp/images/test.png",
				},
			},
			width: 100,
			checks: func(t *testing.T, output string) {
				// Should contain header
				if !strings.Contains(output, "Images attached:") {
					t.Error("output should contain 'Images attached:' header")
				}
				// Should contain image ID
				if !strings.Contains(output, "[img-1]") {
					t.Error("output should contain [img-1]")
				}
				// Should contain dimensions
				if !strings.Contains(output, "800x600") {
					t.Error("output should contain dimensions 800x600")
				}
				// Should contain size
				if !strings.Contains(output, "245KB") {
					t.Error("output should contain size 245KB")
				}
				// Should contain path
				if !strings.Contains(output, ".steiner/tmp/images/test.png") {
					t.Error("output should contain path")
				}
			},
		},
		{
			name: "multiple images",
			rows: []imagesAttachedRowData{
				{
					id:     "img-1",
					width:  800,
					height: 600,
					size:   "245KB",
					path:   ".steiner/tmp/images/test1.png",
				},
				{
					id:     "img-2",
					width:  1920,
					height: 1080,
					size:   "1.2MB",
					path:   ".steiner/tmp/images/test2.png",
				},
			},
			width: 100,
			checks: func(t *testing.T, output string) {
				if !strings.Contains(output, "[img-1]") {
					t.Error("output should contain [img-1]")
				}
				if !strings.Contains(output, "[img-2]") {
					t.Error("output should contain [img-2]")
				}
				if !strings.Contains(output, "800x600") {
					t.Error("output should contain first image dimensions")
				}
				if !strings.Contains(output, "1920x1080") {
					t.Error("output should contain second image dimensions")
				}
			},
		},
		{
			name: "long path wraps",
			rows: []imagesAttachedRowData{
				{
					id:     "img-1",
					width:  1920,
					height: 1080,
					size:   "1.5MB",
					path:   ".steiner/tmp/images/very_long_filename_that_should_trigger_wrapping.png",
				},
			},
			width: 40,
			checks: func(t *testing.T, output string) {
				// With narrow width, should wrap
				lines := strings.Split(strings.TrimSpace(output), "\n")
				if len(lines) < 2 {
					t.Errorf("output should have wrapped to multiple lines at width 40, got %d lines", len(lines))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			segment := contentSegment{
				kind: segmentImagesAttached,
				imagesAttachedData: &imagesAttachedData{
					rows: tc.rows,
				},
			}

			output := b.renderImagesAttachedSegment(segment, tc.width)
			tc.checks(t, output)
		})
	}
}

func TestRenderImagesAttachedSegmentEmpty(t *testing.T) {
	b := &contentBuffer{
		styles: theme.BuildStyles(theme.AccentAmber),
	}

	segment := contentSegment{
		kind:               segmentImagesAttached,
		imagesAttachedData: nil,
	}

	output := b.renderImagesAttachedSegment(segment, 100)
	if output != "" {
		t.Errorf("renderImagesAttachedSegment with nil data should return empty string, got %q", output)
	}
}

func TestRenderImagesAttachedSegmentEmptyRows(t *testing.T) {
	b := &contentBuffer{
		styles: theme.BuildStyles(theme.AccentAmber),
	}

	segment := contentSegment{
		kind: segmentImagesAttached,
		imagesAttachedData: &imagesAttachedData{
			rows: []imagesAttachedRowData{},
		},
	}

	output := b.renderImagesAttachedSegment(segment, 100)
	if output != "" {
		t.Errorf("renderImagesAttachedSegment with empty rows should return empty string, got %q", output)
	}
}
