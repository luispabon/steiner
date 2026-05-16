package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// ollamaDiscoverer discovers model metadata via the Ollama API.
type ollamaDiscoverer struct {
	baseURL    string
	httpClient *http.Client
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

// DiscoverModelMetadata fetches model context length from Ollama.
// Tries model_info.general.context_length first (newer Ollama), then
// falls back to parsing the parameters string for num_ctx.
// Returns zero ModelMetadata on any failure.
func (d *ollamaDiscoverer) DiscoverModelMetadata(ctx context.Context, backendModelID string) (ModelMetadata, error) {
	body, err := json.Marshal(ollamaShowRequest{Model: backendModelID})
	if err != nil {
		return ModelMetadata{}, nil //nolint:nilerr // discovery is best-effort
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/api/show", bytes.NewReader(body))
	if err != nil {
		return ModelMetadata{}, nil //nolint:nilerr // discovery is best-effort
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return ModelMetadata{}, nil //nolint:nilerr // discovery is best-effort
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ModelMetadata{}, nil
	}

	var payload ollamaShowResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ModelMetadata{}, nil //nolint:nilerr // discovery is best-effort
	}

	// Try model_info.general.context_length first (newer Ollama).
	if cw := contextLenFromModelInfo(payload.ModelInfo); cw > 0 {
		return ModelMetadata{ContextWindow: cw}, nil
	}

	// Fall back to parsing the parameters string for num_ctx.
	if cw := contextLenFromParameters(payload.Parameters); cw > 0 {
		return ModelMetadata{ContextWindow: cw}, nil
	}

	return ModelMetadata{}, nil
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
