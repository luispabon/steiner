package advisor

import (
	"context"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/provider"
)

// Request configures a single advisor reasoning pass.
type Request struct {
	Provider     provider.Provider
	Model        string
	Conversation []provider.Message
	MaxTokens    *int
}

// Advise runs one provider-backed reasoning pass over the live conversation snapshot.
func Advise(ctx context.Context, req Request) (provider.ChatResponse, error) {
	if req.Provider == nil {
		return provider.ChatResponse{}, fmt.Errorf("advisor: provider is required")
	}
	if strings.TrimSpace(req.Model) == "" {
		return provider.ChatResponse{}, fmt.Errorf("advisor: model is required")
	}

	response, err := req.Provider.ChatCompletion(ctx, provider.ChatRequest{
		Model:     req.Model,
		Messages:  buildMessages(req.Conversation),
		MaxTokens: req.MaxTokens,
	})
	if err != nil {
		return provider.ChatResponse{}, fmt.Errorf("advisor: %w", err)
	}
	return response, nil
}
