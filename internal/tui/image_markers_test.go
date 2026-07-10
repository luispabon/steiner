package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func TestNextMarkerLabel(t *testing.T) {
	tests := []struct {
		name    string
		markers []imageMarker
		want    string
	}{
		{"empty list", nil, "[Image 1]"},
		{"one marker", []imageMarker{{label: "[Image 1]"}}, "[Image 2]"},
		{"two markers", []imageMarker{{label: "[Image 1]"}, {label: "[Image 2]"}}, "[Image 3]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nextMarkerLabel(tc.markers)
			if got != tc.want {
				t.Errorf("nextMarkerLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPendingImageBlocks(t *testing.T) {
	img1 := agent.ImageBlock{MediaType: "image/png", Data: "abc"}
	img2 := agent.ImageBlock{MediaType: "image/jpeg", Data: "xyz"}

	tests := []struct {
		name    string
		markers []imageMarker
		want    []agent.ImageBlock
	}{
		{"empty", nil, nil},
		{"single", []imageMarker{{label: "[Image 1]", image: img1}}, []agent.ImageBlock{img1}},
		{"preserves order", []imageMarker{{label: "[Image 1]", image: img1}, {label: "[Image 2]", image: img2}}, []agent.ImageBlock{img1, img2}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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
	tests := []struct {
		name   string
		value  string
		marker imageMarker
		want   string
	}{
		{"removes first occurrence", "hello [Image 1] world", imageMarker{label: "[Image 1]"}, "hello  world"},
		{"missing label is noop", "hello world", imageMarker{label: "[Image 1]"}, "hello world"},
		{"removes only first", "[Image 1] foo [Image 1]", imageMarker{label: "[Image 1]"}, " foo [Image 1]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := removeMarkerFromValue(tc.value, tc.marker)
			if got != tc.want {
				t.Errorf("removeMarkerFromValue = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenumberMarkers(t *testing.T) {
	img1 := agent.ImageBlock{MediaType: "image/png", Data: "a"}
	img2 := agent.ImageBlock{MediaType: "image/jpeg", Data: "b"}
	img3 := agent.ImageBlock{MediaType: "image/webp", Data: "c"}

	tests := []struct {
		name          string
		value         string
		markers       []imageMarker
		wantValue     string
		wantLabels    []string
		wantUnchanged bool
	}{
		{
			name:  "renumber after middle removal",
			value: "a [Image 1] c [Image 3]",
			markers: []imageMarker{
				{label: "[Image 1]", image: img1},
				{label: "[Image 3]", image: img3},
			},
			wantValue:  "a [Image 1] c [Image 2]",
			wantLabels: []string{"[Image 1]", "[Image 2]"},
		},
		{
			name:  "mismatch count returns unchanged",
			value: "[Image 1]",
			markers: []imageMarker{
				{label: "[Image 1]", image: img1},
				{label: "[Image 2]", image: img2},
			},
			wantValue:     "[Image 1]",
			wantLabels:    []string{"[Image 1]", "[Image 2]"},
			wantUnchanged: true,
		},
		{
			name:  "three markers in order",
			value: "[Image 1] [Image 2] [Image 3]",
			markers: []imageMarker{
				{label: "[Image 1]", image: img1},
				{label: "[Image 2]", image: img2},
				{label: "[Image 3]", image: img3},
			},
			wantValue:  "[Image 1] [Image 2] [Image 3]",
			wantLabels: []string{"[Image 1]", "[Image 2]", "[Image 3]"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotValue, gotMarkers := renumberMarkers(tc.value, tc.markers)
			if gotValue != tc.wantValue {
				t.Errorf("renumberMarkers value = %q, want %q", gotValue, tc.wantValue)
			}
			if len(gotMarkers) != len(tc.wantLabels) {
				t.Fatalf("renumberMarkers markers len = %d, want %d", len(gotMarkers), len(tc.wantLabels))
			}
			for i, want := range tc.wantLabels {
				if gotMarkers[i].label != want {
					t.Errorf("markers[%d].label = %q, want %q", i, gotMarkers[i].label, want)
				}
			}
		})
	}
}

func TestCursorRuneOffset(t *testing.T) {
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
			got := cursorRuneOffset(tc.value, tc.row, tc.col)
			if got != tc.want {
				t.Errorf("cursorRuneOffset(%q, %d, %d) = %d, want %d", tc.value, tc.row, tc.col, got, tc.want)
			}
		})
	}
}

func TestMarkerAtCursor(t *testing.T) {
	img1 := agent.ImageBlock{MediaType: "image/png", Data: "a"}
	markers := []imageMarker{{label: "[Image 1]", image: img1}}

	// "[Image 1]" is 9 chars, starts at rune 6 in "hello [Image 1] world"
	// rune offsets: h=0,e=1,l=2,l=3,o=4, =5,[=6,I=7,m=8,a=9,g=10,e=11, =12,1=13,]=14, =15
	value := "hello [Image 1] world"

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
		{"at end of marker", 15, 0, false, true, false},
		{"after marker", 16, -1, false, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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
}

func TestReconcileMarkers(t *testing.T) {
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
			value:      "[Image 1] [Image 2]",
			markers:    []imageMarker{{label: "[Image 1]", image: img1}, {label: "[Image 2]", image: img2}},
			wantValue:  "[Image 1] [Image 2]",
			wantLabels: []string{"[Image 1]", "[Image 2]"},
		},
		{
			name:       "missing marker removed from slice",
			value:      "[Image 1]",
			markers:    []imageMarker{{label: "[Image 1]", image: img1}, {label: "[Image 2]", image: img2}},
			wantValue:  "[Image 1]",
			wantLabels: []string{"[Image 1]"},
		},
		{
			name:       "survivors renumbered",
			value:      "[Image 2]",
			markers:    []imageMarker{{label: "[Image 1]", image: img1}, {label: "[Image 2]", image: img2}},
			wantValue:  "[Image 1]",
			wantLabels: []string{"[Image 1]"},
		},
		{
			name:       "no markers",
			value:      "no images here",
			markers:    nil,
			wantValue:  "no images here",
			wantLabels: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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
	inp := newModelInput()
	inp.SetValue("describe this")
	m := Model{
		input: inp,
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
		},
		sidebar: sidebarState{
			workingDir: "/home/user/project",
			homeDir:    "/home/user",
		},
		imageMarkers: []imageMarker{
			{
				label: "[Image 1]",
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

	updated, _ := m.executeSubmitAction("describe this", "describe this", "describe this")
	got := updated.(Model)

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
	img1 := agent.ImageBlock{MediaType: "image/png", Data: "a"}
	markers := []imageMarker{{label: "[Image 1]", image: img1}}
	// value: "ab[Image 1]cd"
	// rune positions: a=0,b=1,[=2,...]=10,c=11,d=12
	value := "ab[Image 1]cd"

	tests := []struct {
		name      string
		offset    int
		direction int
		want      int
	}{
		{"before marker unchanged", 1, 1, 1},
		{"at start unchanged", 2, 1, 2},
		{"inside snap right", 5, 1, 11},
		{"inside snap left", 5, -1, 2},
		{"at end unchanged", 11, 1, 11},
		{"after marker unchanged", 12, 1, 12},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := snapCursorPastMarkers(value, tc.offset, markers, tc.direction)
			if got != tc.want {
				t.Errorf("snapCursorPastMarkers(%d, %d) = %d, want %d", tc.offset, tc.direction, got, tc.want)
			}
		})
	}
}
