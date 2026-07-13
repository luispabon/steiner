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

func (c *Client) withRetry(
	ctx context.Context,
	operation func(attempt int) (bool, error),
	classify func(error) retryDecision,
	onRetry func(retryAttemptInfo),
) error {
	if c == nil {
		return fmt.Errorf("provider is not initialized")
	}
	maxAttempts := c.retryAttempts()
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
			info.Delay = c.retryBackoffDelay(attempt)
		}

		if onRetry != nil {
			onRetry(info)
		}

		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.sleepForRetry(ctx, info.Delay); err != nil {
			return err
		}
	}

	return fmt.Errorf("retry loop exhausted")
}

func (c *Client) retryAttempts() int {
	if c == nil || !c.retry.Enabled || c.retry.MaxAttempts <= 1 {
		return 1
	}
	return c.retry.MaxAttempts
}

func (c *Client) retryBackoffDelay(attempt int) time.Duration {
	if c == nil || c.retry.InitialBackoff <= 0 {
		return 0
	}
	cap := c.retry.InitialBackoff
	for i := 1; i < attempt; i++ {
		if c.retry.MaxBackoff > 0 && cap >= c.retry.MaxBackoff {
			cap = c.retry.MaxBackoff
			break
		}
		if cap > time.Duration(1<<62) {
			break
		}
		cap *= 2
	}
	if c.retry.MaxBackoff > 0 && cap > c.retry.MaxBackoff {
		cap = c.retry.MaxBackoff
	}
	if cap <= 0 {
		return 0
	}
	if c.jitter != nil {
		return c.jitter(cap)
	}
	return c.fullJitter(cap)
}

func (c *Client) sleepForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	if c != nil && c.sleep != nil {
		return c.sleep(ctx, delay)
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
