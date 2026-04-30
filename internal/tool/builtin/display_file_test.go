package builtin

import (
	"context"
	"encoding/json"
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
			NormalizeDisplayFile(&got)
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
