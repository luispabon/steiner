package mcp

import (
	"strings"
	"testing"
)

func TestSchemaTooLarge(t *testing.T) {
	big := map[string]any{"type": "object", "description": strings.Repeat("a", maxInputSchemaBytes)}
	tests := []struct {
		name   string
		schema any
		want   bool
	}{
		{"small", map[string]any{"type": "object"}, false},
		{"nil", nil, false},
		{"oversized", big, true},
		{"unmarshalable", map[string]any{"c": make(chan int)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, got := schemaTooLarge(tt.schema); got != tt.want {
				t.Errorf("schemaTooLarge = %v, want %v", got, tt.want)
			}
		})
	}
}
