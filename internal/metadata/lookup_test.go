package metadata

import "testing"

func TestLookup_Found(t *testing.T) {
	data := []byte(`{"openai":{"models":{"gpt-4o":{"limit":{"context":128000,"output":16384}}}}}`)
	info := Lookup(data, "gpt-4o")
	if info.ContextWindow != 128000 {
		t.Errorf("ContextWindow: got %d, want 128000", info.ContextWindow)
	}
	if info.MaxOutputTokens != 16384 {
		t.Errorf("MaxOutputTokens: got %d, want 16384", info.MaxOutputTokens)
	}
}

func TestLookup_NotFound(t *testing.T) {
	data := []byte(`{"openai":{"models":{"gpt-4o":{"limit":{"context":128000,"output":16384}}}}}`)
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
	data := []byte(`{"openai":{"id":"openai"}}`)
	info := Lookup(data, "gpt-4o")
	if info.ContextWindow != 0 || info.MaxOutputTokens != 0 {
		t.Errorf("expected zero ModelInfo for missing 'models' key, got %+v", info)
	}
}

func TestLookup_PartialFields(t *testing.T) {
	data := []byte(`{"anthropic":{"models":{"claude-3":{"limit":{"context":200000}}}}}`)
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

func TestLookup_AcrossProviders(t *testing.T) {
	data := []byte(`{
		"provider-a":{"models":{"model-x":{"limit":{"context":1000}}}},
		"provider-b":{"models":{"model-y":{"limit":{"context":2000,"output":500}}}}
	}`)
	info := Lookup(data, "model-y")
	if info.ContextWindow != 2000 {
		t.Errorf("ContextWindow: got %d, want 2000", info.ContextWindow)
	}
	if info.MaxOutputTokens != 500 {
		t.Errorf("MaxOutputTokens: got %d, want 500", info.MaxOutputTokens)
	}
}

func TestLookupWithProviderPrefersProviderSpecificLimits(t *testing.T) {
	data := []byte(`{
		"opencode-go":{"models":{"deepseek-v4-flash":{"limit":{"context":1000000,"output":384000}}}},
		"ollama-cloud":{"models":{"deepseek-v4-flash":{"limit":{"context":1048576,"output":1048576}}}}
	}`)
	info := LookupWithProvider(data, "opencode-go", "deepseek-v4-flash")
	if info.ContextWindow != 1000000 {
		t.Errorf("ContextWindow: got %d, want 1000000", info.ContextWindow)
	}
	if info.MaxOutputTokens != 384000 {
		t.Errorf("MaxOutputTokens: got %d, want 384000", info.MaxOutputTokens)
	}
}

func TestLookupWithProviderFallsBackAcrossProviders(t *testing.T) {
	data := []byte(`{
		"opencode-go":{"models":{"deepseek-v4-flash":{"limit":{"context":1000000,"output":384000}}}}
	}`)
	info := LookupWithProvider(data, "local", "deepseek-v4-flash")
	if info.ContextWindow != 1000000 {
		t.Errorf("ContextWindow: got %d, want 1000000", info.ContextWindow)
	}
	if info.MaxOutputTokens != 384000 {
		t.Errorf("MaxOutputTokens: got %d, want 384000", info.MaxOutputTokens)
	}
}

func TestLookupWithProviderIncludesTransportMetadata(t *testing.T) {
	data := []byte(`{
		"opencode-go":{
			"npm":"@ai-sdk/openai-compatible",
			"api":"https://opencode.ai/zen/go/v1/",
			"models":{
				"minimax-m3":{
					"limit":{"context":256000,"output":8192},
					"provider":{"npm":"@ai-sdk/anthropic","api":"https://api.minimax.chat/anthropic"},
					"interleaved":{"field":"reasoning_content"}
				},
				"kimi-k2.6":{
					"provider":{"npm":"@ai-sdk/openai-compatible","api":"https://api.moonshot.ai/v1"}
				}
			}
		}
	}`)

	t.Run("model-level provider metadata wins", func(t *testing.T) {
		info := LookupWithProvider(data, "opencode-go", "minimax-m3")
		if got, want := info.ProviderNPM, "@ai-sdk/openai-compatible"; got != want {
			t.Fatalf("ProviderNPM = %q, want %q", got, want)
		}
		if got, want := info.ProviderAPI, "https://opencode.ai/zen/go/v1/"; got != want {
			t.Fatalf("ProviderAPI = %q, want %q", got, want)
		}
		if got, want := info.ModelProviderNPM, "@ai-sdk/anthropic"; got != want {
			t.Fatalf("ModelProviderNPM = %q, want %q", got, want)
		}
		if got, want := info.ModelProviderAPI, "https://api.minimax.chat/anthropic"; got != want {
			t.Fatalf("ModelProviderAPI = %q, want %q", got, want)
		}
		if got, want := info.InterleavedField, "reasoning_content"; got != want {
			t.Fatalf("InterleavedField = %q, want %q", got, want)
		}
		if !info.ReasoningEchoBack {
			t.Fatal("ReasoningEchoBack = false, want true")
		}
	})

	t.Run("provider-level metadata is preserved without model override", func(t *testing.T) {
		info := LookupWithProvider(data, "opencode-go", "kimi-k2.6")
		if got, want := info.ProviderNPM, "@ai-sdk/openai-compatible"; got != want {
			t.Fatalf("ProviderNPM = %q, want %q", got, want)
		}
		if got, want := info.ModelProviderNPM, "@ai-sdk/openai-compatible"; got != want {
			t.Fatalf("ModelProviderNPM = %q, want %q", got, want)
		}
		if got, want := info.ModelProviderAPI, "https://api.moonshot.ai/v1"; got != want {
			t.Fatalf("ModelProviderAPI = %q, want %q", got, want)
		}
	})
}

func TestLookup_ReasoningEchoBack(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		modelID string
		want    bool
	}{
		{
			name:    "interleaved field is reasoning_content",
			data:    []byte(`{"deepseek":{"models":{"deepseek-r1":{"limit":{"context":128000},"interleaved":{"field":"reasoning_content"}}}}}`),
			modelID: "deepseek-r1",
			want:    true,
		},
		{
			name:    "interleaved field is something else",
			data:    []byte(`{"prov":{"models":{"model-x":{"limit":{"context":128000},"interleaved":{"field":"other"}}}}}`),
			modelID: "model-x",
			want:    false,
		},
		{
			name:    "no interleaved key",
			data:    []byte(`{"openai":{"models":{"gpt-4o":{"limit":{"context":128000}}}}}`),
			modelID: "gpt-4o",
			want:    false,
		},
		{
			name:    "interleaved without field",
			data:    []byte(`{"prov":{"models":{"model-y":{"limit":{"context":128000},"interleaved":{}}}}}`),
			modelID: "model-y",
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := Lookup(tt.data, tt.modelID)
			if info.ReasoningEchoBack != tt.want {
				t.Errorf("ReasoningEchoBack=%v, want %v", info.ReasoningEchoBack, tt.want)
			}
		})
	}
}

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

func TestLookup_VisionInput(t *testing.T) {
	tests := []struct {
		name            string
		data            []byte
		modelID         string
		wantFound       bool
		wantVisionInput bool
	}{
		{
			name:            "image modality present",
			data:            []byte(`{"prov":{"models":{"gpt-4v":{"modalities":{"input":["text","image"]}}}}}`),
			modelID:         "gpt-4v",
			wantFound:       true,
			wantVisionInput: true,
		},
		{
			name:            "image modality absent",
			data:            []byte(`{"prov":{"models":{"deepseek":{"modalities":{"input":["text"]}}}}}`),
			modelID:         "deepseek",
			wantFound:       true,
			wantVisionInput: false,
		},
		{
			name:            "model not found in dataset",
			data:            []byte(`{"prov":{"models":{"gpt-4o":{"limit":{"context":128000}}}}}`),
			modelID:         "unknown-model",
			wantFound:       false,
			wantVisionInput: false,
		},
		{
			name:            "case-insensitive image modality matching",
			data:            []byte(`{"prov":{"models":{"model-x":{"modalities":{"input":["text","IMAGE"]}}}}}`),
			modelID:         "model-x",
			wantFound:       true,
			wantVisionInput: true,
		},
		{
			name:            "no modalities field",
			data:            []byte(`{"prov":{"models":{"model-y":{"limit":{"context":128000}}}}}`),
			modelID:         "model-y",
			wantFound:       true,
			wantVisionInput: false,
		},
		{
			name:            "empty modalities input array",
			data:            []byte(`{"prov":{"models":{"model-z":{"modalities":{"input":[]}}}}}`),
			modelID:         "model-z",
			wantFound:       true,
			wantVisionInput: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := Lookup(tt.data, tt.modelID)
			if info.Found != tt.wantFound {
				t.Errorf("Found=%v, want %v", info.Found, tt.wantFound)
			}
			if info.VisionInput != tt.wantVisionInput {
				t.Errorf("VisionInput=%v, want %v", info.VisionInput, tt.wantVisionInput)
			}
		})
	}
}
