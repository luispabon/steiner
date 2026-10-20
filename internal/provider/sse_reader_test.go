package provider

import (
	"bufio"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
)

type sseFrame struct {
	event string
	data  string
}

func TestReadSSEFrame(t *testing.T) {
	errBoom := errors.New("boom")
	tests := []struct {
		name    string
		input   string
		tail    error // when set, returned after input instead of EOF
		want    []sseFrame
		wantErr error
	}{
		{
			name:    "single data line",
			input:   "data: hello world\n\n",
			want:    []sseFrame{{"", "hello world"}},
			wantErr: io.EOF,
		},
		{
			name:    "multi-line data joined with newline",
			input:   "data: line1\ndata: line2\n\n",
			want:    []sseFrame{{"", "line1\nline2"}},
			wantErr: io.EOF,
		},
		{
			name:    "event name with data",
			input:   "event: message_start\ndata: {}\n\n",
			want:    []sseFrame{{"message_start", "{}"}},
			wantErr: io.EOF,
		},
		{
			name:    "event-only frame is returned",
			input:   "event: ping\n\n",
			want:    []sseFrame{{"ping", ""}},
			wantErr: io.EOF,
		},
		{
			name:    "event-only frame followed by data frame",
			input:   "event: ping\n\ndata: x\n\n",
			want:    []sseFrame{{"ping", ""}, {"", "x"}},
			wantErr: io.EOF,
		},
		{
			name:    "event name does not carry across frames",
			input:   "event: a\ndata: 1\n\ndata: 2\n\n",
			want:    []sseFrame{{"a", "1"}, {"", "2"}},
			wantErr: io.EOF,
		},
		{
			name:    "leading blank lines are skipped",
			input:   "\n\ndata: x\n\n",
			want:    []sseFrame{{"", "x"}},
			wantErr: io.EOF,
		},
		{
			name:    "comment lines are ignored",
			input:   ": keepalive\ndata: x\n\n",
			want:    []sseFrame{{"", "x"}},
			wantErr: io.EOF,
		},
		{
			name:    "id and retry fields are ignored",
			input:   "id: 7\nretry: 100\ndata: x\n\n",
			want:    []sseFrame{{"", "x"}},
			wantErr: io.EOF,
		},
		{
			name:    "CRLF line endings",
			input:   "event: e\r\ndata: x\r\n\r\n",
			want:    []sseFrame{{"e", "x"}},
			wantErr: io.EOF,
		},
		{
			name:    "field values are trimmed",
			input:   "event:  e \ndata:   spaced  \n\n",
			want:    []sseFrame{{"e", "spaced"}},
			wantErr: io.EOF,
		},
		{
			name:    "DONE sentinel is passed through as data",
			input:   "data: [DONE]\n\n",
			want:    []sseFrame{{"", "[DONE]"}},
			wantErr: io.EOF,
		},
		{
			name:    "empty data value is not a frame",
			input:   "data:\n\n",
			want:    nil,
			wantErr: io.EOF,
		},
		{
			name:    "EOF ends a data frame without trailing blank line",
			input:   "data: a\n\ndata: tail",
			want:    []sseFrame{{"", "a"}, {"", "tail"}},
			wantErr: io.EOF,
		},
		{
			name:    "EOF ends an event-only frame without trailing blank line",
			input:   "event: ping\n",
			want:    []sseFrame{{"ping", ""}},
			wantErr: io.EOF,
		},
		{
			name:    "empty input is EOF",
			input:   "",
			want:    nil,
			wantErr: io.EOF,
		},
		{
			name:    "only blank lines is EOF",
			input:   "\n\n\n",
			want:    nil,
			wantErr: io.EOF,
		},
		{
			name:    "read error discards partial frame",
			input:   "data: partial\n",
			tail:    errBoom,
			want:    nil,
			wantErr: errBoom,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var src io.Reader = strings.NewReader(tc.input)
			if tc.tail != nil {
				src = io.MultiReader(src, iotest.ErrReader(tc.tail))
			}
			reader := bufio.NewReader(src)

			var got []sseFrame
			var gotErr error
			for {
				event, data, err := readSSEFrame(reader)
				if err != nil {
					gotErr = err
					break
				}
				got = append(got, sseFrame{event, data})
			}

			if !errors.Is(gotErr, tc.wantErr) {
				t.Fatalf("error = %v, want %v", gotErr, tc.wantErr)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("frames = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// readSSEEvent (openai and codex) skips frames without data, so an event-only
// frame must not surface as an empty payload.
func TestReadSSEEventSkipsFramesWithoutData(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "event-only frame is skipped", input: "event: ping\n\ndata: x\n\n", want: "x"},
		{name: "event name with data", input: "event: e\ndata: x\n\n", want: "x"},
		{name: "trailing event-only frame ends at EOF", input: "data: x\n\nevent: ping\n", want: "x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := bufio.NewReader(strings.NewReader(tc.input))
			got, err := readSSEEvent(reader)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if got, err := readSSEEvent(reader); !errors.Is(err, io.EOF) || got != "" {
				t.Fatalf("after payload: got (%q, %v), want (\"\", io.EOF)", got, err)
			}
		})
	}
}
