package builtin

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateDiagnosticTextPreservesUTF8Boundary(t *testing.T) {
	input := strings.Repeat("é", 100)
	got := truncateDiagnosticText(input, 40)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateDiagnosticText() = %q, want valid UTF-8", got)
	}
	if !strings.HasSuffix(got, diagnosticTruncationMarker) {
		t.Fatalf("truncateDiagnosticText() = %q, want truncation marker", got)
	}
	if !strings.HasPrefix(got, "é") {
		t.Fatalf("truncateDiagnosticText() = %q, want complete UTF-8 prefix", got)
	}
}
