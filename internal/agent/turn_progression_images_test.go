package agent

import (
	"strings"
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
	got := stripImagesFromMessages(msgs, false)
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
	got := stripImagesFromMessages(msgs, false)
	if got[0].Images != nil {
		t.Fatal("expected Images to be nil after stripping")
	}
	// The user's words are recorded once, before any placeholder is appended.
	if got[0].RequestText != "two images" {
		t.Fatalf("RequestText = %q, want the user's words once", got[0].RequestText)
	}
	if !got[0].VisionRouted {
		t.Fatal("VisionRouted = false, want true")
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
	got := stripImagesFromMessages(msgs, false)
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
	got := stripImagesFromMessages(msgs, false)
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
	got := stripImagesFromMessages(msgs, false)
	// No Data to strip, Content must not grow.
	if got[0].Content != msgs[0].Content {
		t.Fatalf("Content changed for already-stripped image: got %q, want %q", got[0].Content, msgs[0].Content)
	}
}

// TestStripImagesFromMessagesDoesNotRecordTwice proves image handling records
// the user's words only on the first strip: a text turn keeps them, an image-only
// turn keeps recording nothing rather than adopting its placeholder, a later
// image on the same message appends only its own placeholder, and re-stripping a
// stripped message changes nothing.
func TestStripImagesFromMessagesDoesNotRecordTwice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{name: "text turn keeps its words", content: "look at this image"},
		{name: "image-only turn keeps recording nothing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			first := stripImagesFromMessages([]Message{{
				Role:    MessageRoleUser,
				Content: tt.content,
				Images:  []ImageBlock{{MediaType: "image/png", Data: "aaa", Width: 10, Height: 10, SizeBytes: 1024}},
			}}, false)[0]
			if first.RequestText != tt.content {
				t.Fatalf("first strip RequestText = %q, want %q", first.RequestText, tt.content)
			}

			// An image arriving later joins the already-routed message.
			first.Images = []ImageBlock{{MediaType: "image/png", Data: "bbb", Width: 20, Height: 20, SizeBytes: 2048}}
			second := stripImagesFromMessages([]Message{first}, false)[0]
			if second.RequestText != tt.content {
				t.Fatalf("second strip RequestText = %q, want %q, not the placeholder text", second.RequestText, tt.content)
			}
			want := "[image: 10x10 png 1KB]\n[image: 20x20 png 2KB]"
			if tt.content != "" {
				want = tt.content + "\n" + want
			}
			if second.Content != want {
				t.Fatalf("Content = %q, want %q", second.Content, want)
			}

			// Once the image data is gone, another pass changes nothing.
			third := stripImagesFromMessages([]Message{second}, false)[0]
			if third.Content != second.Content || third.RequestText != second.RequestText || len(third.Images) != 0 {
				t.Fatalf("third strip = (%q, RequestText %q, %d images), want (%q, %q, 0)", third.Content, third.RequestText, len(third.Images), second.Content, second.RequestText)
			}
		})
	}
}

func TestImageBlockPlaceholder_LegacyFormat(t *testing.T) {
	tests := []struct {
		name   string
		img    ImageBlock
		vision bool
		want   string
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
			got := imageBlockPlaceholder(tt.img, tt.vision)
			if got != tt.want {
				t.Fatalf("imageBlockPlaceholder() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestImageBlockPlaceholder_NewFormatWithIDAndFilePath(t *testing.T) {
	tests := []struct {
		name   string
		img    ImageBlock
		vision bool
		want   string
	}{
		{
			name: "image with ID and file path, vision sub-agent available",
			img: ImageBlock{
				ID:        "img-1",
				FilePath:  "/home/user/.steiner/tmp/images/screenshot.png",
				MediaType: "image/png",
				Width:     1920,
				Height:    1080,
				SizeBytes: 456789,
			},
			vision: true,
			want:   `[image img-1: /home/user/.steiner/tmp/images/screenshot.png 1920x1080 png 446KB; use the read tool, or sub_agent type "vision" with image_id "img-1", to re-examine]`,
		},
		{
			name: "image with ID and no vision sub-agent",
			img: ImageBlock{
				ID:        "img-1",
				FilePath:  "/home/user/.steiner/tmp/images/screenshot.png",
				MediaType: "image/png",
				Width:     1920,
				Height:    1080,
				SizeBytes: 456789,
			},
			want: `[image img-1: /home/user/.steiner/tmp/images/screenshot.png 1920x1080 png 446KB; use the read tool to re-examine]`,
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
			vision: true,
			want:   `[image img-2: /tmp/test.jpg 640x480 jpeg; use the read tool, or sub_agent type "vision" with image_id "img-2", to re-examine]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := imageBlockPlaceholder(tt.img, tt.vision)
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
	got := stripImagesFromMessages(msgs, false)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Images != nil {
		t.Fatalf("Images = %v, want nil", got[0].Images)
	}
	// Should contain the new format placeholder with the read tool hint.
	wantPlaceholder := `[image img-1: /home/user/.steiner/tmp/images/test.png 1920x1080 png 488KB; use the read tool to re-examine]`
	wantContent := "look at this image\n" + wantPlaceholder
	if got[0].Content != wantContent {
		t.Fatalf("Content = %q, want %q", got[0].Content, wantContent)
	}
	// Original must not be mutated.
	if msgs[0].Images[0].Data == "" {
		t.Fatal("original message Data was mutated")
	}
}

// TestFreezeImagePlaceholdersStableAcrossStrip proves the delivery-time freeze
// and the later byte strip agree: the token is appended once before delivery,
// recorded in message metadata, and stripping leaves the text untouched.
func TestFreezeImagePlaceholdersStableAcrossStrip(t *testing.T) {
	img := ImageBlock{ID: "img-1", FilePath: "/tmp/a.png", MediaType: "image/png", Data: "AAAA", Width: 10, Height: 20, SizeBytes: 2048}
	msgs := []Message{{Role: MessageRoleUser, Content: "look", Images: []ImageBlock{img}}}

	frozen := freezeImagePlaceholders(msgs, false)
	wantContent := "look\n" + imageBlockPlaceholder(img, false)
	if frozen[0].Content != wantContent {
		t.Fatalf("frozen Content = %q, want %q", frozen[0].Content, wantContent)
	}
	if len(frozen[0].ImagePlaceholders) != 1 || frozen[0].ImagePlaceholders[0] != imageBlockPlaceholder(img, false) {
		t.Fatalf("ImagePlaceholders = %v, want the frozen token", frozen[0].ImagePlaceholders)
	}
	if len(frozen[0].Images) != 1 || frozen[0].Images[0].Data != "AAAA" {
		t.Fatalf("freeze removed image data: %#v", frozen[0].Images)
	}

	// A second freeze pass must not duplicate text or metadata.
	again := freezeImagePlaceholders(frozen, false)
	if again[0].Content != wantContent || len(again[0].ImagePlaceholders) != 1 {
		t.Fatalf("second freeze = (%q, %v), want (%q, one token)", again[0].Content, again[0].ImagePlaceholders, wantContent)
	}

	stripped := stripImagesFromMessages(again, false)
	if stripped[0].Content != wantContent {
		t.Fatalf("stripped Content = %q, want the frozen text %q", stripped[0].Content, wantContent)
	}
	if stripped[0].Images != nil {
		t.Fatalf("stripped Images = %#v, want nil", stripped[0].Images)
	}
	// The original delivery message must not be mutated by the strip.
	if len(frozen[0].Images) != 1 || frozen[0].Images[0].Data != "AAAA" {
		t.Fatalf("strip mutated its input: %#v", frozen[0].Images)
	}
}

// TestFreezeImagePlaceholdersRecordsRequestBeforeTokens proves the freeze
// records the user's own words before it appends model-facing tokens, and that
// an image-only turn records no request text at all.
func TestFreezeImagePlaceholdersRecordsRequestBeforeTokens(t *testing.T) {
	img := ImageBlock{ID: "img-1", FilePath: "/tmp/a.png", MediaType: "image/png", Data: "AAAA", Width: 10, Height: 20}
	tests := []struct {
		name        string
		content     string
		wantRequest string
	}{
		{name: "text is recorded once", content: "look at this", wantRequest: "look at this"},
		{name: "image-only turn records nothing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := Message{Role: MessageRoleUser, Content: tt.content, Images: []ImageBlock{img}}
			frozen := freezeImagePlaceholders([]Message{msg}, false)[0]
			if frozen.RequestText != tt.wantRequest {
				t.Fatalf("RequestText after freeze = %q, want %q", frozen.RequestText, tt.wantRequest)
			}
			if !frozen.VisionRouted {
				t.Fatal("VisionRouted = false after freeze, want true")
			}
			stripped := stripImagesFromMessages([]Message{frozen}, false)[0]
			if stripped.RequestText != tt.wantRequest {
				t.Fatalf("RequestText after strip = %q, want %q", stripped.RequestText, tt.wantRequest)
			}
			if strings.Contains(stripped.RequestText, "image") {
				t.Fatalf("RequestText adopted the placeholder: %q", stripped.RequestText)
			}
		})
	}
}

// TestFreezeImagePlaceholdersIgnoresUserTextCollision proves a user message that
// already contains the placeholder token still gets exactly one frozen token per
// image: idempotence comes from recorded metadata, not text matching.
func TestFreezeImagePlaceholdersIgnoresUserTextCollision(t *testing.T) {
	img := ImageBlock{ID: "img-1", FilePath: "/tmp/a.png", MediaType: "image/png", Data: "AAAA", Width: 10, Height: 20}
	token := imageBlockPlaceholder(img, false)
	msgs := []Message{{Role: MessageRoleUser, Content: "compare with " + token, Images: []ImageBlock{img}}}

	frozen := freezeImagePlaceholders(msgs, false)
	if want := "compare with " + token + "\n" + token; frozen[0].Content != want {
		t.Fatalf("Content = %q, want %q", frozen[0].Content, want)
	}
	if len(frozen[0].ImagePlaceholders) != 1 {
		t.Fatalf("ImagePlaceholders = %v, want one recorded token", frozen[0].ImagePlaceholders)
	}
}

// TestFreezeImagePlaceholdersKeepsDuplicateImages proves two copies of one image
// get two placeholder tokens and two recorded entries.
func TestFreezeImagePlaceholdersKeepsDuplicateImages(t *testing.T) {
	img := ImageBlock{ID: "img-1", FilePath: "/tmp/a.png", MediaType: "image/png", Data: "AAAA", Width: 10, Height: 20}
	msgs := []Message{{Role: MessageRoleUser, Images: []ImageBlock{img, img}}}

	frozen := freezeImagePlaceholders(msgs, false)
	token := imageBlockPlaceholder(img, false)
	if want := token + "\n" + token; frozen[0].Content != want {
		t.Fatalf("Content = %q, want %q", frozen[0].Content, want)
	}
	if len(frozen[0].ImagePlaceholders) != 2 {
		t.Fatalf("ImagePlaceholders = %v, want two entries", frozen[0].ImagePlaceholders)
	}
}

// TestStripImagesFromMessagesRecordsUserRequest proves an image strip records
// the user's own words before it appends a placeholder, and marks only real user
// messages: tool output and sourced delivery wrappers keep their current
// behaviour.
func TestStripImagesFromMessagesRecordsUserRequest(t *testing.T) {
	t.Parallel()

	image := ImageBlock{ID: "img-1", FilePath: "/tmp/img.png", MediaType: "image/png", Data: "base64data", Width: 10, Height: 10, SizeBytes: 1024}
	tests := []struct {
		name        string
		message     Message
		wantRequest string
		wantRouted  bool
	}{
		{
			name:        "user text is recorded",
			message:     Message{Role: MessageRoleUser, Content: "look at this image", Images: []ImageBlock{image}},
			wantRequest: "look at this image",
			wantRouted:  true,
		},
		{
			name:        "delivery wrapper is stripped from the record",
			message:     Message{Role: MessageRoleUser, Content: "<steiner-sub-agents-pending>code-1 (code, running)</steiner-sub-agents-pending>\n\nlook at this image", Images: []ImageBlock{image}},
			wantRequest: "look at this image",
			wantRouted:  true,
		},
		{
			name:       "image-only turn records nothing",
			message:    Message{Role: MessageRoleUser, Images: []ImageBlock{image}},
			wantRouted: true,
		},
		{
			name:        "recorded request is kept",
			message:     Message{Role: MessageRoleUser, Content: "look at this image", RequestText: "recorded request", Images: []ImageBlock{image}},
			wantRequest: "recorded request",
			wantRouted:  true,
		},
		{
			name:    "tool message stays unmarked",
			message: Message{Role: MessageRoleTool, Name: "read", Content: "tool output", Images: []ImageBlock{image}},
		},
		{
			name:    "sourced message stays unmarked",
			message: Message{Role: MessageRoleUser, Source: MessageSourceSubAgentResult, Content: "results", Images: []ImageBlock{image}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripImagesFromMessages([]Message{tt.message}, false)[0]
			if got.RequestText != tt.wantRequest {
				t.Fatalf("RequestText = %q, want %q", got.RequestText, tt.wantRequest)
			}
			if got.VisionRouted != tt.wantRouted {
				t.Fatalf("VisionRouted = %t, want %t", got.VisionRouted, tt.wantRouted)
			}
			if got.Images != nil {
				t.Fatalf("Images = %v, want nil", got.Images)
			}
		})
	}
}

// TestFreezeImagePlaceholdersHintsOnlyAvailableRoutes proves the frozen token
// advertises the vision sub-agent route only when one is configured, and keeps
// the read route either way.
func TestFreezeImagePlaceholdersHintsOnlyAvailableRoutes(t *testing.T) {
	img := ImageBlock{ID: "img-1", FilePath: "/tmp/a.png", MediaType: "image/png", Data: "AAAA", Width: 1, Height: 1}
	tests := []struct {
		name            string
		visionAvailable bool
		wantSubAgent    bool
	}{
		{name: "delegation disabled", visionAvailable: false, wantSubAgent: false},
		{name: "vision sub-agent available", visionAvailable: true, wantSubAgent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frozen := freezeImagePlaceholders([]Message{{Role: MessageRoleUser, Content: "look", Images: []ImageBlock{img}}}, tt.visionAvailable)[0]
			if !strings.Contains(frozen.Content, "use the read tool") {
				t.Fatalf("Content = %q, want the read route kept", frozen.Content)
			}
			if got := strings.Contains(frozen.Content, "sub_agent type"); got != tt.wantSubAgent {
				t.Fatalf("sub-agent hint present = %t, want %t in %q", got, tt.wantSubAgent, frozen.Content)
			}
		})
	}
}
