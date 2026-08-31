package builtin

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

func TestDisplayFileSchemaRejectsLanguage(t *testing.T) {
	schema := DisplayFileSchema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("DisplayFileSchema() missing properties map")
	}
	if _, ok := props["language"]; ok {
		t.Fatal("DisplayFileSchema() unexpectedly exposes language property")
	}
	for _, key := range []string{"path", "offset", "limit"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("DisplayFileSchema() missing %q property", key)
		}
	}
	if got, ok := schema["additionalProperties"].(bool); !ok || got {
		t.Fatalf("DisplayFileSchema() additionalProperties = %v, want false", schema["additionalProperties"])
	}

	_, err := decodeInput[DisplayFileInput](map[string]any{
		"path":     "note.txt",
		"language": "go",
	})
	if err == nil {
		t.Fatal("decodeInput(display_file) = nil error, want unknown field error")
	}
	if !strings.Contains(err.Error(), "unknown field \"language\"") {
		t.Fatalf("decodeInput(display_file) error = %v, want unknown field language", err)
	}
}

func TestDisplayFileSchemaAcceptsPathOffsetLimit(t *testing.T) {
	in, err := decodeInput[DisplayFileInput](map[string]any{
		"path":   "note.txt",
		"offset": 3,
		"limit":  9,
	})
	if err != nil {
		t.Fatalf("decodeInput(display_file) error = %v", err)
	}
	if got, want := in.Path, "note.txt"; got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
	if got, want := in.Offset, 3; got != want {
		t.Fatalf("Offset = %d, want %d", got, want)
	}
	if got, want := in.Limit, 9; got != want {
		t.Fatalf("Limit = %d, want %d", got, want)
	}
}

func TestNormalizeDisplayFile(t *testing.T) {
	tests := []struct {
		name string
		in   DisplayFileInput
		want DisplayFileInput
	}{
		{
			name: "negative offset and missing limit",
			in:   DisplayFileInput{Path: "note.txt", Offset: -4},
			want: DisplayFileInput{Path: "note.txt", Offset: 1, Limit: defaultDisplayFileLimit},
		},
		{
			name: "zero offset and zero limit",
			in:   DisplayFileInput{Path: "note.txt"},
			want: DisplayFileInput{Path: "note.txt", Offset: 1, Limit: defaultDisplayFileLimit},
		},
		{
			name: "large limit capped",
			in:   DisplayFileInput{Path: "note.txt", Offset: 2, Limit: maxDisplayFileLimit + 100},
			want: DisplayFileInput{Path: "note.txt", Offset: 2, Limit: maxDisplayFileLimit},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in
			normalizeDisplayFile(&got)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("NormalizeDisplayFile() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestDisplayFileToolEmitsPreviewAndMetadataOnlyResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snippet.go")
	if err := os.WriteFile(path, []byte("package main\nfunc main() {}\nfmt.Println(\"hi\")\n// tail\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	var events []output.Event
	env := Env{
		WorkDir:     dir,
		PathPolicy:  &policy,
		Interactive: true,
		EventSink: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
	}

	result, err := NewDisplayFileTool(env).Handler(context.Background(), map[string]any{
		"path":   "snippet.go",
		"offset": 2,
		"limit":  2,
	})
	if err != nil {
		t.Fatalf("display_file handler error = %v", err)
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal(result) error = %v", err)
	}
	if strings.Contains(string(data), "package main") || strings.Contains(string(data), "fmt.Println") {
		t.Fatalf("result JSON leaks file contents: %s", data)
	}

	got, ok := result.(*DisplayFileResult)
	if !ok {
		t.Fatalf("result type = %T, want *DisplayFileResult", result)
	}
	if got.Status != "displayed" {
		t.Fatalf("Status = %q, want displayed", got.Status)
	}
	if got.Path != "snippet.go" {
		t.Fatalf("Path = %q, want snippet.go", got.Path)
	}
	if got.Message != "" {
		t.Fatalf("Message = %q, want empty", got.Message)
	}

	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	payload, ok := events[0].Payload.(output.DisplayFilePayload)
	if !ok {
		t.Fatalf("event payload type = %T, want output.DisplayFilePayload", events[0].Payload)
	}
	if payload.Offset != 2 {
		t.Fatalf("payload.Offset = %d, want 2", payload.Offset)
	}
	if payload.Limit != 2 {
		t.Fatalf("payload.Limit = %d, want 2", payload.Limit)
	}
	if payload.Preview.Language != "go" {
		t.Fatalf("payload.Preview.Language = %q, want go", payload.Preview.Language)
	}
	if got := flattenPreviewLines(payload.Preview.Lines); got != "func main() {}\nfmt.Println(\"hi\")" {
		t.Fatalf("preview lines = %q, want sliced file contents", got)
	}
	if !payload.Preview.Truncated {
		t.Fatal("payload.Preview.Truncated = false, want true")
	}
}

func TestDisplayFileToolHandlesImageAndBinaryFilesSafely(t *testing.T) {
	dir := t.TempDir()
	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	var events []output.Event
	env := Env{
		WorkDir:     dir,
		PathPolicy:  &policy,
		Interactive: true,
		EventSink: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
	}

	writeImage := func(name string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatalf("Create(%s) error = %v", name, err)
		}
		img := image.NewRGBA(image.Rect(0, 0, 1, 1))
		img.Set(0, 0, color.RGBA{255, 0, 0, 255})
		if err := png.Encode(file, img); err != nil {
			t.Fatalf("png.Encode(%s) error = %v", name, err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("Close(%s) error = %v", name, err)
		}
		return name
	}

	imageResult, err := NewDisplayFileTool(env).Handler(context.Background(), map[string]any{
		"path": writeImage("image.png"),
	})
	if err != nil {
		t.Fatalf("display_file image handler error = %v", err)
	}
	gotImage, ok := imageResult.(*DisplayFileResult)
	if !ok {
		t.Fatalf("image result type = %T, want *DisplayFileResult", imageResult)
	}
	if gotImage.Status != "displayed" {
		t.Fatalf("image status = %q, want displayed", gotImage.Status)
	}
	if gotImage.Message != "" {
		t.Fatalf("image message = %q, want empty", gotImage.Message)
	}
	imageJSON, err := json.Marshal(imageResult)
	if err != nil {
		t.Fatalf("json.Marshal(image result) error = %v", err)
	}
	if strings.Contains(string(imageJSON), "\x89PNG") {
		t.Fatalf("image result JSON leaks raw bytes: %s", imageJSON)
	}
	if len(events) != 1 {
		t.Fatalf("image events len = %d, want 1", len(events))
	}
	imagePayload, ok := events[0].Payload.(output.DisplayFilePayload)
	if !ok {
		t.Fatalf("image payload type = %T, want output.DisplayFilePayload", events[0].Payload)
	}
	if got := flattenPreviewLines(imagePayload.Preview.Lines); !strings.Contains(got, "image preview unavailable") {
		t.Fatalf("image preview text = %q, want safe placeholder", got)
	}

	events = events[:0]
	binaryPath := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(binaryPath, []byte{0x00, 0x01, 0x02, 0x03, 0x04}, 0o644); err != nil {
		t.Fatalf("WriteFile(binary) error = %v", err)
	}

	binaryResult, err := NewDisplayFileTool(env).Handler(context.Background(), map[string]any{
		"path": "blob.bin",
	})
	if err != nil {
		t.Fatalf("display_file binary handler error = %v", err)
	}
	gotBinary, ok := binaryResult.(*DisplayFileResult)
	if !ok {
		t.Fatalf("binary result type = %T, want *DisplayFileResult", binaryResult)
	}
	if gotBinary.Status != "displayed" {
		t.Fatalf("binary status = %q, want displayed", gotBinary.Status)
	}
	if gotBinary.Message != "" {
		t.Fatalf("binary message = %q, want empty", gotBinary.Message)
	}
	binaryJSON, err := json.Marshal(binaryResult)
	if err != nil {
		t.Fatalf("json.Marshal(binary result) error = %v", err)
	}
	if strings.Contains(string(binaryJSON), "\x00") {
		t.Fatalf("binary result JSON leaks raw bytes: %q", binaryJSON)
	}
	if len(events) != 1 {
		t.Fatalf("binary events len = %d, want 1", len(events))
	}
	binaryPayload, ok := events[0].Payload.(output.DisplayFilePayload)
	if !ok {
		t.Fatalf("binary payload type = %T, want output.DisplayFilePayload", events[0].Payload)
	}
	if got := flattenPreviewLines(binaryPayload.Preview.Lines); !strings.Contains(got, "binary file omitted") {
		t.Fatalf("binary preview text = %q, want safe placeholder", got)
	}
}

func TestDisplayFileToolNonTUIFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("content\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	env := Env{
		WorkDir:     dir,
		PathPolicy:  &policy,
		Interactive: false,
	}

	result, err := NewDisplayFileTool(env).Handler(context.Background(), map[string]any{
		"path": "note.txt",
	})
	if err != nil {
		t.Fatalf("display_file handler error = %v", err)
	}

	got, ok := result.(*DisplayFileResult)
	if !ok {
		t.Fatalf("result type = %T, want *DisplayFileResult", result)
	}
	if got.Status != "unavailable" {
		t.Fatalf("Status = %q, want unavailable", got.Status)
	}
	if got.Path != "note.txt" {
		t.Fatalf("Path = %q, want note.txt", got.Path)
	}
	if !strings.Contains(got.Message, "read") {
		t.Fatalf("Message = %q, want to contain 'read'", got.Message)
	}
}

func flattenPreviewLines(lines []output.PreviewLine) string {
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		for _, span := range line.Spans {
			b.WriteString(span.Text)
		}
	}
	return b.String()
}
