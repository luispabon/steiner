package metadata

import "testing"

func TestLookup_Found(t *testing.T) {
	data := []byte(`{"models":{"gpt-4o":{"context":128000,"maxOutputTokens":16384}}}`)
	info := Lookup(data, "gpt-4o")
	if info.ContextWindow != 128000 {
		t.Errorf("ContextWindow: got %d, want 128000", info.ContextWindow)
	}
	if info.MaxOutputTokens != 16384 {
		t.Errorf("MaxOutputTokens: got %d, want 16384", info.MaxOutputTokens)
	}
}

func TestLookup_NotFound(t *testing.T) {
	data := []byte(`{"models":{"gpt-4o":{"context":128000,"maxOutputTokens":16384}}}`)
	info := Lookup(data, "unknown-model")
	if info.ContextWindow != 0 || info.MaxOutputTokens != 0 {
		t.Errorf("expected zero ModelInfo, got %+v", info)
	}
}

func TestLookup_MalformedJSON(t *testing.T) {
	info := Lookup([]byte(`not json`), "gpt-4o")
	if info.ContextWindow != 0 || info.MaxOutputTokens != 0 {
		t.Errorf("expected zero ModelInfo on malformed JSON, got %+v", info)
	}
}

func TestLookup_MissingModelsKey(t *testing.T) {
	data := []byte(`{"something_else":{}}`)
	info := Lookup(data, "gpt-4o")
	if info.ContextWindow != 0 || info.MaxOutputTokens != 0 {
		t.Errorf("expected zero ModelInfo for missing 'models' key, got %+v", info)
	}
}

func TestLookup_PartialFields(t *testing.T) {
	// Only context present, no maxOutputTokens.
	data := []byte(`{"models":{"claude-3":{"context":200000}}}`)
	info := Lookup(data, "claude-3")
	if info.ContextWindow != 200000 {
		t.Errorf("ContextWindow: got %d, want 200000", info.ContextWindow)
	}
	if info.MaxOutputTokens != 0 {
		t.Errorf("expected zero MaxOutputTokens, got %d", info.MaxOutputTokens)
	}
}

func TestLookup_EmptyData(t *testing.T) {
	info := Lookup([]byte{}, "gpt-4o")
	if info.ContextWindow != 0 || info.MaxOutputTokens != 0 {
		t.Errorf("expected zero ModelInfo for empty data, got %+v", info)
	}
}

func TestCountModels(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want int
	}{
		{
			name: "counts models",
			data: []byte(`{"models":{"gpt-4o":{},"gpt-4.1":{}}}`),
			want: 2,
		},
		{
			name: "missing models key",
			data: []byte(`{"something_else":{}}`),
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
