package provider

import (
	"context"
	"errors"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

// claudeSubStream runs one turn in a goroutine and streams its chunks. It emits
// exactly one provider-stream diagnostics record when the turn ends, whatever
// the outcome. The record carries no request or response content.
func claudeSubStream(ctx context.Context, pool *ClaudeSubscriptionPool, req ChatRequest, streamErrorLog *StreamErrorLogger) (<-chan ChatChunk, error) {
	out := make(chan ChatChunk)
	go func() {
		defer close(out)
		call := providerCallInput{
			log:       streamErrorLog,
			provider:  string(config.ProviderTypeClaudeSubscription),
			model:     req.Model,
			transport: "cli",
			callKind:  claudeSubCallKind(req),
			start:     time.Now(),
			ctx:       ctx,
		}
		var (
			chunks       int
			firstChunkAt time.Time
		)
		err := claudeSubTurn(ctx, pool, req, func(chunk ChatChunk) error {
			select {
			case out <- chunk:
				if chunks == 0 {
					firstChunkAt = time.Now()
				}
				chunks++
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		if chunks > 0 {
			call.ttft = firstChunkAt.Sub(call.start)
		}
		call.attempts = 1
		call.chunks = chunks
		call.partialStream = chunks > 0 && err != nil
		call.err = claudeSubDiagnosticError(err)
		emitProviderCall(call)
		if err != nil {
			sendChunk(ctx, out, ChatChunk{Done: true, Error: err.Error(), OriginalError: err})
		}
	}()
	return out, nil
}

// claudeSubDetailError is a CLI failure whose full message quotes CLI output:
// terminal result text, the stderr tail, or a control-response error. The
// caller gets detail, which is returned to the user. The diagnostics record
// gets only safe, which names the failure without that output.
type claudeSubDetailError struct {
	detail error
	safe   error
}

func (e *claudeSubDetailError) Error() string { return e.detail.Error() }

func (e *claudeSubDetailError) Unwrap() error { return e.detail }

// claudeSubDiagnosticError returns the error a provider diagnostics record may
// carry for err. A chain holding a claudeSubDetailError yields its safe form;
// any other error is returned unchanged.
func claudeSubDiagnosticError(err error) error {
	var detail *claudeSubDetailError
	if errors.As(err, &detail) {
		return detail.safe
	}
	return err
}

// claudeSubCallKind names the kind of call req makes for the diagnostics
// record. Precedence is advisor, summary, sub-agent, then normal, so a request
// that matches several gets the first.
func claudeSubCallKind(req ChatRequest) string {
	switch {
	case req.AdvisorCacheProfile:
		return "advisor"
	case req.CompactionSummary:
		return "summary"
	case req.ParentTransportSession != "":
		return "sub_agent"
	default:
		return "normal"
	}
}
