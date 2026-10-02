package agent

import "testing"

func TestToolBatchSeq(t *testing.T) {
	tests := []struct {
		name   string
		id     string
		want   uint64
		wantOK bool
	}{
		{"call id", "call#7", 7, true},
		{"empty call id form", "batch#3", 3, true},
		{"call id containing separator", "a#b#12", 12, true},
		{"missing suffix", "call", 0, false},
		{"empty suffix", "call#", 0, false},
		{"non numeric", "call#x", 0, false},
		{"negative", "call#-1", 0, false},
		{"empty", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ToolBatchSeq(tt.id)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("ToolBatchSeq(%q) = %d, %v; want %d, %v", tt.id, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestNewToolBatchIDRoundTripsAndIncreases(t *testing.T) {
	var prev uint64
	for _, first := range []string{"call_0", "", "x#y"} {
		n, ok := ToolBatchSeq(newToolBatchID(first))
		if !ok || n <= prev {
			t.Fatalf("newToolBatchID(%q) seq = %d, %v; want increasing after %d", first, n, ok, prev)
		}
		prev = n
	}
}
