package tool

import (
	"strings"
	"testing"
)

func TestTruncationMarkerUsesShownAndTotal(t *testing.T) {
	capture := StreamCapture{
		Bytes:     128,
		Shown:     24,
		Truncated: true,
		Preview:   "tail",
	}
	got := capture.Summary()
	if !strings.Contains(got, "<truncated output shown=24 total=128>") {
		t.Fatalf("Summary() = %q, want shown/total marker", got)
	}
	if strings.Contains(got, "bytes=") {
		t.Fatalf("Summary() = %q, want no bytes= marker", got)
	}
}
