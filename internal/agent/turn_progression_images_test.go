package agent

import (
	"testing"
)

func TestStripImagesFromMessages_basic(t *testing.T) {
	msgs := []Message{
		{
			Role:    MessageRoleUser,
			Content: "look at this",
			Images: []ImageBlock{
				{MediaType: "image/png", Data: "abc123", Width: 100, Height: 200, SizeBytes: 2048},
			},
		},
	}
	got := stripImagesFromMessages(msgs, VisionUnknown, false)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Images != nil {
		t.Fatalf("Images = %v, want nil", got[0].Images)
	}
	wantContent := "look at this\n[image: 100x200 png 2KB]"
	if got[0].Content != wantContent {
		t.Fatalf("Content = %q, want %q", got[0].Content, wantContent)
	}
	// Original must not be mutated.
	if msgs[0].Images[0].Data == "" {
		t.Fatal("original message Data was mutated")
	}
}

func TestStripImagesFromMessages_multiple(t *testing.T) {
	msgs := []Message{
		{
			Role:    MessageRoleUser,
			Content: "two images",
			Images: []ImageBlock{
				{MediaType: "image/png", Data: "aaa", Width: 640, Height: 480, SizeBytes: 50000},
				{MediaType: "image/jpeg", Data: "bbb", Width: 1920, Height: 1080, SizeBytes: 2097152},
			},
		},
	}
	got := stripImagesFromMessages(msgs, VisionUnknown, false)
	if got[0].Images != nil {
		t.Fatal("expected Images to be nil after stripping")
	}
	wantContent := "two images\n[image: 640x480 png 48KB]\n[image: 1920x1080 jpeg 2.0MB]"
	if got[0].Content != wantContent {
		t.Fatalf("Content = %q, want %q", got[0].Content, wantContent)
	}
}

func TestStripImagesFromMessages_noImages(t *testing.T) {
	msgs := []Message{
		{Role: MessageRoleUser, Content: "plain text"},
		{Role: MessageRoleAssistant, Content: "response"},
	}
	got := stripImagesFromMessages(msgs, VisionUnknown, false)
	for i, m := range got {
		if m.Content != msgs[i].Content {
			t.Fatalf("msg[%d].Content changed: got %q, want %q", i, m.Content, msgs[i].Content)
		}
	}
}

func TestStripImagesFromMessages_preservesContent(t *testing.T) {
	msgs := []Message{
		{
			Role:    MessageRoleTool,
			Content: "existing tool output",
			Images: []ImageBlock{
				{MediaType: "image/png", Data: "data", Width: 0, Height: 0, SizeBytes: 0},
			},
		},
	}
	got := stripImagesFromMessages(msgs, VisionUnknown, false)
	wantContent := "existing tool output\n[image: ? png]"
	if got[0].Content != wantContent {
		t.Fatalf("Content = %q, want %q", got[0].Content, wantContent)
	}
}

func TestStripImagesFromMessages_alreadyStripped(t *testing.T) {
	msgs := []Message{
		{
			Role:    MessageRoleUser,
			Content: "already stripped\n[image: 100x100 png 1KB]",
			Images: []ImageBlock{
				{MediaType: "image/png", Data: "", Width: 100, Height: 100, SizeBytes: 1024},
			},
		},
	}
	got := stripImagesFromMessages(msgs, VisionUnknown, false)
	// No Data to strip, Content must not grow.
	if got[0].Content != msgs[0].Content {
		t.Fatalf("Content changed for already-stripped image: got %q, want %q", got[0].Content, msgs[0].Content)
	}
}

func TestImageBlockPlaceholder_LegacyFormat(t *testing.T) {
	tests := []struct {
		name string
		img  ImageBlock
		want string
	}{
		{
			name: "basic image without size",
			img: ImageBlock{
				MediaType: "image/png",
				Width:     100,
				Height:    200,
				SizeBytes: 0,
			},
			want: "[image: 100x200 png]",
		},
		{
			name: "image with KB size",
			img: ImageBlock{
				MediaType: "image/png",
				Width:     100,
				Height:    200,
				SizeBytes: 2048,
			},
			want: "[image: 100x200 png 2KB]",
		},
		{
			name: "image with MB size",
			img: ImageBlock{
				MediaType: "image/jpeg",
				Width:     1920,
				Height:    1080,
				SizeBytes: 2097152,
			},
			want: "[image: 1920x1080 jpeg 2.0MB]",
		},
		{
			name: "image without dimensions",
			img: ImageBlock{
				MediaType: "image/png",
				SizeBytes: 1024,
			},
			want: "[image: ? png 1KB]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := imageBlockPlaceholder(tt.img, VisionUnknown, false)
			if got != tt.want {
				t.Fatalf("imageBlockPlaceholder() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestImageBlockPlaceholder_NewFormatWithIDAndFilePath(t *testing.T) {
	tests := []struct {
		name string
		img  ImageBlock
		want string
	}{
		{
			name: "image with ID and file path",
			img: ImageBlock{
				ID:        "img-1",
				FilePath:  "/home/user/.steiner/tmp/images/screenshot.png",
				MediaType: "image/png",
				Width:     1920,
				Height:    1080,
				SizeBytes: 456789,
			},
			want: `[image img-1: /home/user/.steiner/tmp/images/screenshot.png 1920x1080 png 446KB — use read tool to re-examine]`,
		},
		{
			name: "image with ID but no size",
			img: ImageBlock{
				ID:        "img-2",
				FilePath:  "/tmp/test.jpg",
				MediaType: "image/jpeg",
				Width:     640,
				Height:    480,
				SizeBytes: 0,
			},
			want: `[image img-2: /tmp/test.jpg 640x480 jpeg — use read tool to re-examine]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := imageBlockPlaceholder(tt.img, VisionUnknown, false)
			if got != tt.want {
				t.Fatalf("imageBlockPlaceholder() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStripImagesFromMessages_withIDAndFilePath(t *testing.T) {
	msgs := []Message{
		{
			Role:    MessageRoleUser,
			Content: "look at this image",
			Images: []ImageBlock{
				{
					ID:        "img-1",
					FilePath:  "/home/user/.steiner/tmp/images/test.png",
					MediaType: "image/png",
					Data:      "base64data123",
					Width:     1920,
					Height:    1080,
					SizeBytes: 500000,
				},
			},
		},
	}
	got := stripImagesFromMessages(msgs, VisionUnknown, false)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Images != nil {
		t.Fatalf("Images = %v, want nil", got[0].Images)
	}
	// Should contain the new format placeholder with the read tool hint.
	wantPlaceholder := `[image img-1: /home/user/.steiner/tmp/images/test.png 1920x1080 png 488KB — use read tool to re-examine]`
	wantContent := "look at this image\n" + wantPlaceholder
	if got[0].Content != wantContent {
		t.Fatalf("Content = %q, want %q", got[0].Content, wantContent)
	}
	// Original must not be mutated.
	if msgs[0].Images[0].Data == "" {
		t.Fatal("original message Data was mutated")
	}
}

func TestImageBlockPlaceholder_VisionCapable(t *testing.T) {
	img := ImageBlock{
		ID:        "img-1",
		FilePath:  "/home/user/.steiner/tmp/images/screenshot.png",
		MediaType: "image/png",
		Width:     1920,
		Height:    1080,
		SizeBytes: 456789,
	}
	got := imageBlockPlaceholder(img, VisionCapable, false)
	want := `[image img-1: /home/user/.steiner/tmp/images/screenshot.png 1920x1080 png 446KB — use read tool to re-examine]`
	if got != want {
		t.Fatalf("VisionCapable: got %q, want %q", got, want)
	}
}

func TestImageBlockPlaceholder_VisionCapableWithSubAgent(t *testing.T) {
	img := ImageBlock{
		ID:        "img-1",
		FilePath:  "/home/user/.steiner/tmp/images/screenshot.png",
		MediaType: "image/png",
		Width:     1920,
		Height:    1080,
		SizeBytes: 456789,
	}
	got := imageBlockPlaceholder(img, VisionCapable, true)
	want := `[image img-1: /home/user/.steiner/tmp/images/screenshot.png 1920x1080 png 446KB — use sub_agent type "vision" with image_id "img-1" or read tool to re-examine]`
	if got != want {
		t.Fatalf("VisionCapable with sub-agent: got %q, want %q", got, want)
	}
}

func TestImageBlockPlaceholder_VisionIncapableWithSubAgent(t *testing.T) {
	img := ImageBlock{
		ID:        "img-1",
		FilePath:  "/home/user/.steiner/tmp/images/screenshot.png",
		MediaType: "image/png",
		Width:     1920,
		Height:    1080,
		SizeBytes: 456789,
	}
	got := imageBlockPlaceholder(img, VisionIncapable, true)
	want := `[image img-1: /home/user/.steiner/tmp/images/screenshot.png 1920x1080 png 446KB — use follow_up with the agent_id from the image analysis]`
	if got != want {
		t.Fatalf("VisionIncapable with sub-agent: got %q, want %q", got, want)
	}
}

func TestImageBlockPlaceholder_VisionIncapableWithoutSubAgent(t *testing.T) {
	img := ImageBlock{
		ID:        "img-1",
		FilePath:  "/home/user/.steiner/tmp/images/screenshot.png",
		MediaType: "image/png",
		Width:     1920,
		Height:    1080,
		SizeBytes: 456789,
	}
	got := imageBlockPlaceholder(img, VisionIncapable, false)
	want := `[image img-1: /home/user/.steiner/tmp/images/screenshot.png 1920x1080 png 446KB]`
	if got != want {
		t.Fatalf("VisionIncapable without sub-agent: got %q, want %q", got, want)
	}
}

func TestStripImagesFromMessages_VisionIncapableWithSubAgent(t *testing.T) {
	msgs := []Message{
		{
			Role:    MessageRoleUser,
			Content: "look at this image",
			Images: []ImageBlock{
				{
					ID:        "img-1",
					FilePath:  "/home/user/.steiner/tmp/images/test.png",
					MediaType: "image/png",
					Data:      "base64data123",
					Width:     1920,
					Height:    1080,
					SizeBytes: 500000,
				},
			},
		},
	}
	got := stripImagesFromMessages(msgs, VisionIncapable, true)
	if got[0].Images != nil {
		t.Fatalf("Images should be nil")
	}
	wantPlaceholder := `[image img-1: /home/user/.steiner/tmp/images/test.png 1920x1080 png 488KB — use follow_up with the agent_id from the image analysis]`
	wantContent := "look at this image\n" + wantPlaceholder
	if got[0].Content != wantContent {
		t.Fatalf("got %q, want %q", got[0].Content, wantContent)
	}
}

func TestStripImagesFromMessages_VisionIncapableWithoutSubAgent(t *testing.T) {
	msgs := []Message{
		{
			Role:    MessageRoleUser,
			Content: "look at this image",
			Images: []ImageBlock{
				{
					ID:        "img-1",
					FilePath:  "/home/user/.steiner/tmp/images/test.png",
					MediaType: "image/png",
					Data:      "base64data123",
					Width:     1920,
					Height:    1080,
					SizeBytes: 500000,
				},
			},
		},
	}
	got := stripImagesFromMessages(msgs, VisionIncapable, false)
	if got[0].Images != nil {
		t.Fatalf("Images should be nil")
	}
	wantPlaceholder := `[image img-1: /home/user/.steiner/tmp/images/test.png 1920x1080 png 488KB]`
	wantContent := "look at this image\n" + wantPlaceholder
	if got[0].Content != wantContent {
		t.Fatalf("got %q, want %q", got[0].Content, wantContent)
	}
}
