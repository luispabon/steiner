package metadata

import "testing"

func TestCountModels(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want int
	}{
		{
			name: "counts unique models across providers",
			data: []byte(`{
				"prov-a":{"models":{"gpt-4o":{},"gpt-4.1":{}}},
				"prov-b":{"models":{"gpt-4o":{},"claude-3":{}}}
			}`),
			want: 3,
		},
		{
			name: "missing models key",
			data: []byte(`{"prov":{"id":"prov"}}`),
			want: 0,
		},
		{
			name: "malformed json",
			data: []byte(`not json`),
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CountModels(tt.data); got != tt.want {
				t.Fatalf("CountModels() = %d, want %d", got, tt.want)
			}
		})
	}
}
