package provider

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type retryDecision struct {
	retry      bool
	reason     string
	retryAfter time.Duration
}

type retryAttemptInfo struct {
	Attempt       int
	MaxAttempts   int
	Delay         time.Duration
	Reason        string
	PartialStream bool
}

func (p *OpenAICompat) withRetry(
	ctx context.Context,
	operation func(attempt int) (bool, error),
	classify func(error) retryDecision,
	onRetry func(retryAttemptInfo),
) error {
	if p == nil {
		return fmt.Errorf("provider is not initialized")
	}
	maxAttempts := p.retryAttempts()
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		partialStream, err := operation(attempt)
		if err == nil {
			return nil
		}

		decision := classify(err)
		if !decision.retry || attempt == maxAttempts {
			return err
		}

		info := retryAttemptInfo{
			Attempt:       attempt + 1,
			MaxAttempts:   maxAttempts,
			Reason:        decision.reason,
			PartialStream: partialStream,
		}
		if decision.retryAfter > 0 {
			info.Delay = decision.retryAfter
		} else {
			info.Delay = p.retryBackoffDelay(attempt)
		}

		if onRetry != nil {
			onRetry(info)
		}

		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.sleepForRetry(ctx, info.Delay); err != nil {
			return err
		}
	}

	return fmt.Errorf("retry loop exhausted")
}

func (p *OpenAICompat) retryAttempts() int {
	if p == nil || !p.retry.Enabled || p.retry.MaxAttempts <= 1 {
		return 1
	}
	return p.retry.MaxAttempts
}

func (p *OpenAICompat) retryBackoffDelay(attempt int) time.Duration {
	if p == nil || p.retry.InitialBackoff <= 0 {
		return 0
	}
	cap := p.retry.InitialBackoff
	for i := 1; i < attempt; i++ {
		if p.retry.MaxBackoff > 0 && cap >= p.retry.MaxBackoff {
			cap = p.retry.MaxBackoff
			break
		}
		if cap > time.Duration(1<<62) {
			break
		}
		cap *= 2
	}
	if p.retry.MaxBackoff > 0 && cap > p.retry.MaxBackoff {
		cap = p.retry.MaxBackoff
	}
	if cap <= 0 {
		return 0
	}
	if p.jitter != nil {
		return p.jitter(cap)
	}
	return p.fullJitter(cap)
}

func (p *OpenAICompat) sleepForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	if p != nil && p.sleep != nil {
		return p.sleep(ctx, delay)
	}
	return defaultRetrySleep(ctx, delay)
}

func asHTTPError(err error) *HTTPError {
	if err == nil {
		return nil
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr
	}
	return nil
}
