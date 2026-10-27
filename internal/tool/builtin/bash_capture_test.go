package builtin

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

const capMarker = "<<MARK>>"

func TestCaptureUntilMarkerBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		head, tail int
		wantHead   string
		wantTail   string
		wantTotal  int64
		wantTrunc  bool
	}{
		{"empty", "", 4, 4, "", "", 0, false},
		{"under limits", "abc", 4, 4, "abc", "", 3, false},
		{"exact head", "abcd", 4, 4, "abcd", "", 4, false},
		{"head+1 overlaps tail", "abcde", 4, 4, "abcd", "e", 5, true},
		{"overlap trimmed", "abcdef", 4, 4, "abcd", "ef", 6, true},
		{"exact head+tail no gap", "abcdefgh", 4, 4, "abcd", "efgh", 8, true},
		{"gap", "abcdefghij", 4, 4, "abcd", "ghij", 10, true},
		{"no newline utf8", "héllo wörld ✓", 3, 4, "h\xc3\xa9", "\x20\xe2\x9c\x93", 17, true},
		{"zero tail", "abcdef", 2, 0, "ab", "", 6, true},
		{"zero head", "abcdef", 0, 2, "", "ef", 6, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input + capMarker + "rest"))
			got, err := captureUntilMarker(r, capMarker, tt.head, tt.tail)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if string(got.Head) != tt.wantHead || string(got.Tail) != tt.wantTail ||
				got.TotalBytes != tt.wantTotal || got.HeadTruncated != tt.wantTrunc {
				t.Fatalf("got head=%q tail=%q total=%d trunc=%v", got.Head, got.Tail, got.TotalBytes, got.HeadTruncated)
			}
			rest, _ := io.ReadAll(r)
			if string(rest) != "rest" {
				t.Fatalf("rest = %q, want %q", rest, "rest")
			}
		})
	}
}

func TestCaptureUntilMarkerSplitAcrossChunks(t *testing.T) {
	input := "line one\nno newline tail" + capMarker + "after"
	for _, rd := range []func(io.Reader) io.Reader{iotest.OneByteReader, iotest.HalfReader, iotest.DataErrReader} {
		r := bufio.NewReaderSize(rd(strings.NewReader(input)), 16)
		got, err := captureUntilMarker(r, capMarker, 1024, 1024)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if string(got.Head) != "line one\nno newline tail" || len(got.Tail) != 0 {
			t.Fatalf("head=%q tail=%q", got.Head, got.Tail)
		}
		rest, _ := io.ReadAll(r)
		if string(rest) != "after" {
			t.Fatalf("rest = %q", rest)
		}
	}
}

func TestCaptureUntilMarkerPartialMarkerIsData(t *testing.T) {
	input := "a<<MARa<<MARK>" + capMarker
	r := bufio.NewReaderSize(iotest.OneByteReader(strings.NewReader(input)), 16)
	got, err := captureUntilMarker(r, capMarker, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Head) != "a<<MARa<<MARK>" {
		t.Fatalf("head = %q", got.Head)
	}
}

func TestCaptureUntilMarkerReaderErrorKeepsPartial(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name string
		in   string
		err  error
	}{{"eof", "abcdef<<MA", io.EOF}, {"error", "abcdef<<MA", boom}} {
		t.Run(tc.name, func(t *testing.T) {
			rd := io.MultiReader(strings.NewReader(tc.in), iotest.ErrReader(tc.err))
			got, err := captureUntilMarker(bufio.NewReader(rd), capMarker, 4, 4)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			// Unconfirmed marker prefix is flushed as data.
			if string(got.Head) != "abcd" || string(got.Tail) != "<<MA" || got.TotalBytes != 10 {
				t.Fatalf("head=%q tail=%q total=%d", got.Head, got.Tail, got.TotalBytes)
			}
		})
	}
}

func TestCaptureUntilMarkerHighVolumeDrains(t *testing.T) {
	const n = 5 << 20
	data := make([]byte, n)
	for i := range data {
		data[i] = byte('a' + i%26)
	}
	input := append(data, []byte(capMarker)...)
	r := bufio.NewReader(bytes.NewReader(input))
	got, err := captureUntilMarker(r, capMarker, 1<<20, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalBytes != n || !got.HeadTruncated || len(got.Head) != 1<<20 || len(got.Tail) != 1<<20 {
		t.Fatalf("total=%d trunc=%v head=%d tail=%d", got.TotalBytes, got.HeadTruncated, len(got.Head), len(got.Tail))
	}
	if !bytes.Equal(got.Head, data[:1<<20]) || !bytes.Equal(got.Tail, data[n-1<<20:]) {
		t.Fatal("head or tail content mismatch")
	}
	if r.Buffered() != 0 {
		t.Fatalf("buffered = %d", r.Buffered())
	}
}
