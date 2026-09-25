package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestMarkerLabel(t *testing.T) {
	t.Parallel()
	m := Model{}

	withID := agent.ImageBlock{ID: "img-3"}
	if got := m.markerLabel(&withID); got != "[img-3]" {
		t.Errorf("markerLabel(with ID) = %q, want %q", got, "[img-3]")
	}

	// Without a store the block has no ID; a TUI-local counter assigns one and
	// stamps it onto the block so the label matches the marker pattern.
	noID := agent.ImageBlock{}
	if got := m.markerLabel(&noID); got != "[img-1]" {
		t.Errorf("markerLabel(no ID) = %q, want %q", got, "[img-1]")
	}
	if noID.ID != "img-1" {
		t.Errorf("block.ID = %q, want img-1", noID.ID)
	}

	second := agent.ImageBlock{}
	if got := m.markerLabel(&second); got != "[img-2]" {
		t.Errorf("markerLabel(second no ID) = %q, want %q", got, "[img-2]")
	}
}

func TestPendingImageBlocks(t *testing.T) {
	t.Parallel()
	img1 := agent.ImageBlock{MediaType: "image/png", Data: "abc"}
	img2 := agent.ImageBlock{MediaType: "image/jpeg", Data: "xyz"}

	tests := []struct {
		name    string
		markers []imageMarker
		want    []agent.ImageBlock
	}{
		{"empty", nil, nil},
		{"single", []imageMarker{{label: "[img-1]", image: img1}}, []agent.ImageBlock{img1}},
		{"preserves order", []imageMarker{{label: "[img-1]", image: img1}, {label: "[img-2]", image: img2}}, []agent.ImageBlock{img1, img2}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := pendingImageBlocks(tc.markers)
			if len(got) != len(tc.want) {
				t.Fatalf("pendingImageBlocks len = %d, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("pendingImageBlocks[%d] = %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestRemoveMarkerFromValue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		value  string
		marker imageMarker
		want   string
	}{
		{"removes first occurrence", "hello [img-1] world", imageMarker{label: "[img-1]"}, "hello  world"},
		{"missing label is noop", "hello world", imageMarker{label: "[img-1]"}, "hello world"},
		{"removes only first", "[img-1] foo [img-1]", imageMarker{label: "[img-1]"}, " foo [img-1]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := removeMarkerFromValue(tc.value, tc.marker)
			if got != tc.want {
				t.Errorf("removeMarkerFromValue = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCursorRuneOffset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		row   int
		col   int
		want  int
	}{
		{"single line start", "hello", 0, 0, 0},
		{"single line mid", "hello", 0, 3, 3},
		{"single line end", "hello", 0, 5, 5},
		{"second line start", "hello\nworld", 1, 0, 6},
		{"second line mid", "hello\nworld", 1, 3, 9},
		{"unicode first line", "héllo\nworld", 0, 2, 2},
		{"unicode second line", "héllo\nwörld", 1, 2, 8},
		{"col clamp", "hi", 0, 10, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cursorRuneOffset(tc.value, tc.row, tc.col)
			if got != tc.want {
				t.Errorf("cursorRuneOffset(%q, %d, %d) = %d, want %d", tc.value, tc.row, tc.col, got, tc.want)
			}
		})
	}
}

func TestMarkerAtCursor(t *testing.T) {
	t.Parallel()
	img1 := agent.ImageBlock{MediaType: "image/png", Data: "a"}
	markers := []imageMarker{{label: "[img-1]", image: img1}}

	// "[img-1]" is 7 chars, starts at rune 6 in "hello [img-1] world"
	// rune offsets: h=0,e=1,l=2,l=3,o=4, =5,[=6,i=7,m=8,g=9,-=10,1=11,]=12, =13
	value := "hello [img-1] world"

	tests := []struct {
		name       string
		offset     int
		wantIdx    int
		wantStart  bool
		wantEnd    bool
		wantInside bool
	}{
		{"before marker", 5, -1, false, false, false},
		{"at start of marker", 6, 0, true, false, false},
		{"inside marker", 8, 0, false, false, true},
		{"at end of marker", 13, 0, false, true, false},
		{"after marker", 14, -1, false, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			idx, atStart, atEnd, inside := markerAtCursor(value, tc.offset, markers)
			if idx != tc.wantIdx {
				t.Errorf("idx = %d, want %d", idx, tc.wantIdx)
			}
			if atStart != tc.wantStart {
				t.Errorf("atStart = %v, want %v", atStart, tc.wantStart)
			}
			if atEnd != tc.wantEnd {
				t.Errorf("atEnd = %v, want %v", atEnd, tc.wantEnd)
			}
			if inside != tc.wantInside {
				t.Errorf("inside = %v, want %v", inside, tc.wantInside)
			}
		})
	}

	// Marker-shaped text with no pending marker is plain text: never reported.
	plain := "say [img-9] now"
	for off := 4; off <= 11; off++ {
		if idx, _, _, _ := markerAtCursor(plain, off, markers); idx != -1 {
			t.Errorf("markerAtCursor(plain text at offset %d) idx = %d, want -1", off, idx)
		}
	}
}

func TestReconcileMarkers(t *testing.T) {
	t.Parallel()
	img1 := agent.ImageBlock{MediaType: "image/png", Data: "a"}
	img2 := agent.ImageBlock{MediaType: "image/jpeg", Data: "b"}

	tests := []struct {
		name       string
		value      string
		markers    []imageMarker
		wantValue  string
		wantLabels []string
	}{
		{
			name:       "all markers present unchanged",
			value:      "[img-1] [img-2]",
			markers:    []imageMarker{{label: "[img-1]", image: img1}, {label: "[img-2]", image: img2}},
			wantValue:  "[img-1] [img-2]",
			wantLabels: []string{"[img-1]", "[img-2]"},
		},
		{
			name:       "missing marker removed from slice without renumbering",
			value:      "[img-1]",
			markers:    []imageMarker{{label: "[img-1]", image: img1}, {label: "[img-2]", image: img2}},
			wantValue:  "[img-1]",
			wantLabels: []string{"[img-1]"},
		},
		{
			name:       "survivor keeps its id",
			value:      "[img-2]",
			markers:    []imageMarker{{label: "[img-1]", image: img1}, {label: "[img-2]", image: img2}},
			wantValue:  "[img-2]",
			wantLabels: []string{"[img-2]"},
		},
		{
			name:       "no markers",
			value:      "no images here",
			markers:    nil,
			wantValue:  "no images here",
			wantLabels: nil,
		},
		{
			name:       "prose with img prefix untouched",
			value:      "Check [img processing notes] for details",
			markers:    nil,
			wantValue:  "Check [img processing notes] for details",
			wantLabels: nil,
		},
		{
			name:       "user typed marker with no pending marker kept",
			value:      "see [img-1]",
			markers:    nil,
			wantValue:  "see [img-1]",
			wantLabels: nil,
		},
		{
			name:       "incomplete numbered marker stripped",
			value:      "Here is [img-3 without close bracket",
			markers:    nil,
			wantValue:  "Here is  without close bracket",
			wantLabels: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotValue, gotMarkers := reconcileMarkers(tc.value, tc.markers)
			if gotValue != tc.wantValue {
				t.Errorf("reconcileMarkers value = %q, want %q", gotValue, tc.wantValue)
			}
			if len(gotMarkers) != len(tc.wantLabels) {
				t.Fatalf("reconcileMarkers markers len = %d, want %d", len(gotMarkers), len(tc.wantLabels))
			}
			for i, want := range tc.wantLabels {
				if gotMarkers[i].label != want {
					t.Errorf("markers[%d].label = %q, want %q", i, gotMarkers[i].label, want)
				}
			}
		})
	}
}

func TestTuiFormatSize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		sizeBytes int
		want      string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1KB"},
		{2048, "2KB"},
		{1024 * 1024, "1.0MB"},
		{1536 * 1024, "1.5MB"},
	}
	for _, tc := range cases {
		got := tuiFormatSize(tc.sizeBytes)
		if got != tc.want {
			t.Errorf("tuiFormatSize(%d) = %q, want %q", tc.sizeBytes, got, tc.want)
		}
	}
}

func TestExecuteSubmitActionAppendsImagesAttached(t *testing.T) {
	t.Parallel()
	inp := newModelInput()
	inp.SetValue("describe this")
	styles := testStyles(theme.AccentAmber)
	m := Model{
		input:  inp,
		styles: styles,
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        styles,
		},
		sidebar: sidebarState{
			workingDir: "/home/user/project",
			homeDir:    "/home/user",
		},
		imageMarkers: []imageMarker{
			{
				label: "[img-1]",
				image: agent.ImageBlock{
					ID:        "img-1",
					FilePath:  "/home/user/project/.steiner/tmp/images/20260630_143052_a7f3.png",
					MediaType: "image/png",
					Width:     2560,
					Height:    1545,
					SizeBytes: 489472, // ~478KB
				},
			},
		},
	}

	updated, _ := m.executeSubmitAction("describe this", "describe this")
	got := updated.(*Model)

	// Check that imageMarkers were cleared
	if len(got.imageMarkers) != 0 {
		t.Errorf("imageMarkers not cleared after submit, got %d", len(got.imageMarkers))
	}

	// Check that segmentImagesAttached was appended to content
	found := false
	for _, seg := range got.content.segments {
		if seg.kind == segmentImagesAttached && seg.imagesAttachedData != nil && len(seg.imagesAttachedData.rows) == 1 {
			row := seg.imagesAttachedData.rows[0]
			if row.id == "img-1" && row.width == 2560 && row.height == 1545 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error(`expected segmentImagesAttached with correct data in content, not found`)
	}
}

func TestSnapCursorPastMarkers(t *testing.T) {
	t.Parallel()
	img1 := agent.ImageBlock{MediaType: "image/png", Data: "a"}
	markers := []imageMarker{{label: "[img-1]", image: img1}}
	// value: "ab[img-1]cd"
	// rune positions: a=0,b=1,[=2,...,]=8,c=9,d=10
	value := "ab[img-1]cd"

	tests := []struct {
		name      string
		offset    int
		direction int
		want      int
	}{
		{"before marker unchanged", 1, 1, 1},
		{"at start unchanged", 2, 1, 2},
		{"inside snap right", 5, 1, 9},
		{"inside snap left", 5, -1, 2},
		{"at end unchanged", 9, 1, 9},
		{"after marker unchanged", 10, 1, 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := snapCursorPastMarkers(value, tc.offset, markers, tc.direction)
			if got != tc.want {
				t.Errorf("snapCursorPastMarkers(%d, %d) = %d, want %d", tc.offset, tc.direction, got, tc.want)
			}
		})
	}

	// A marker-shaped fragment with no pending marker is not snapped over.
	plain := "ab[img-9]cd"
	if got := snapCursorPastMarkers(plain, 5, markers, 1); got != 5 {
		t.Errorf("snapCursorPastMarkers(plain text) = %d, want 5", got)
	}
}
