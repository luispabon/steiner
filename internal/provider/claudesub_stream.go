package provider

import (
	"context"
)

func claudeSubStream(ctx context.Context, pool *ClaudeSubscriptionPool, req ChatRequest) (<-chan ChatChunk, error) {
	out := make(chan ChatChunk)
	go func() {
		defer close(out)
		err := claudeSubTurn(ctx, pool, req, func(chunk ChatChunk) error {
			select {
			case out <- chunk:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		if err != nil {
			sendChunk(ctx, out, ChatChunk{Done: true, Error: err.Error(), OriginalError: err})
		}
	}()
	return out, nil
}
