package tui

import (
	"reflect"
	"testing"
)

func TestLSPStatusSortLSPServerStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []LSPServerStatus
		want  []LSPServerStatus
	}{
		{
			name: "already sorted",
			input: []LSPServerStatus{
				{Name: "go", Root: "/a"},
				{Name: "go", Root: "/b"},
				{Name: "typescript", Root: "/a"},
			},
			want: []LSPServerStatus{
				{Name: "go", Root: "/a"},
				{Name: "go", Root: "/b"},
				{Name: "typescript", Root: "/a"},
			},
		},
		{
			name: "reverse sorted",
			input: []LSPServerStatus{
				{Name: "typescript", Root: "/a"},
				{Name: "go", Root: "/b"},
				{Name: "go", Root: "/a"},
			},
			want: []LSPServerStatus{
				{Name: "go", Root: "/a"},
				{Name: "go", Root: "/b"},
				{Name: "typescript", Root: "/a"},
			},
		},
		{
			name: "tie on name broken by root",
			input: []LSPServerStatus{
				{Name: "go", Root: "/z"},
				{Name: "go", Root: "/a"},
				{Name: "go", Root: "/m"},
			},
			want: []LSPServerStatus{
				{Name: "go", Root: "/a"},
				{Name: "go", Root: "/m"},
				{Name: "go", Root: "/z"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := sortLSPServerStatuses(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("sortLSPServerStatuses() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLSPStatusSortLSPServerStatusesDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	input := []LSPServerStatus{
		{Name: "typescript", Root: "/a"},
		{Name: "go", Root: "/a"},
	}
	original := []LSPServerStatus{
		{Name: "typescript", Root: "/a"},
		{Name: "go", Root: "/a"},
	}

	_ = sortLSPServerStatuses(input)

	if !reflect.DeepEqual(input, original) {
		t.Fatalf("sortLSPServerStatuses mutated input: got %+v, want %+v", input, original)
	}
}
