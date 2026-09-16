package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const discoveryTimeout = 8 * time.Second

// probeSource answers ContextWindow and MaxOutputTokens from a live Ollama
// probe (POST /api/show). It is best-effort: any failure or a non-Ollama
// provider leaves the fields unknown, with no sourceErr. OpenRouter's
// equivalent live probe was replaced by catalogSource; generic OpenAI-compat
// and LM Studio never exposed token limits via their APIs.
type probeSource struct {
	httpClient *http.Client
}

func (probeSource) name() FactSource { return FactSourceDiscovery }

func (s probeSource) resolve(_ context.Context, ref modelRef, want fieldSet) sourceResult {
	if want&fieldSet(fieldContextWindow) == 0 {
		return sourceResult{}
	}
	if !ref.Profile.LiveProbe {
		return sourceResult{}
	}

	httpClient := s.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	// Deliberately derived from context.Background(), not the caller's ctx:
	// probes stay decoupled from the caller's cancellation, matching the
	// pre-fact-resolver discovery behavior.
	probeCtx, cancel := context.WithTimeout(context.Background(), discoveryTimeout)
	defer cancel()
	contextWindow := ollamaProbeContextWindow(probeCtx, ref.Provider.BaseURL, ref.BackendModelID, httpClient)
	if contextWindow <= 0 {
		return sourceResult{}
	}

	var facts ModelFacts
	if want&fieldSet(fieldContextWindow) != 0 {
		facts.ContextWindow = Fact[int]{Value: contextWindow, Known: true, Source: FactSourceDiscovery, Confidence: "medium"}
	}
	return sourceResult{facts: facts}
}

// ollamaShowRequest is the body for POST /api/show.
type ollamaShowRequest struct {
	Model string `json:"model"`
}

// ollamaShowResponse is the response shape for POST /api/show.
type ollamaShowResponse struct {
	ModelInfo  map[string]any `json:"model_info"`
	Parameters string         `json:"parameters"`
}

// ollamaProbeContextWindow fetches model context length from Ollama's
// /api/show endpoint. Tries model_info.general.context_length first (newer
// Ollama), then falls back to parsing the parameters string for num_ctx.
// Returns 0 on any failure.
func ollamaProbeContextWindow(ctx context.Context, baseURL, backendModelID string, httpClient *http.Client) int {
	body, err := json.Marshal(ollamaShowRequest{Model: backendModelID})
	if err != nil {
		return 0
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/show", bytes.NewReader(body))
	if err != nil {
		return 0
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return 0
	}

	var payload ollamaShowResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0
	}

	if cw := contextLenFromModelInfo(payload.ModelInfo); cw > 0 {
		return cw
	}
	return contextLenFromParameters(payload.Parameters)
}

// contextLenFromModelInfo extracts general.context_length from Ollama model_info map.
func contextLenFromModelInfo(info map[string]any) int {
	if info == nil {
		return 0
	}
	v, ok := info["general.context_length"]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// contextLenFromParameters parses the Ollama parameters string for a num_ctx line.
// The format is "num_ctx N\nother_param V\n...".
func contextLenFromParameters(params string) int {
	for _, line := range strings.Split(params, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "num_ctx") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		return n
	}
	return 0
}
