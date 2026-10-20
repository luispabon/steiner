package provider

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

var (
	errRetryTransient = errors.New("transient")
	errRetryFatal     = errors.New("fatal")
)

func classifyRetryTransient(err error) retryDecision {
	if errors.Is(err, errRetryTransient) {
		return retryDecision{retry: true, reason: "transient"}
	}
	return retryDecision{}
}

// newRetryTestClient returns a client with identity jitter so backoff delays
// are exact. A nil sleep leaves the client on defaultRetrySleep.
func newRetryTestClient(retry RetryConfig, sleep func(context.Context, time.Duration) error) *Client {
	return &Client{
		retry:  retry,
		sleep:  sleep,
		jitter: func(capDelay time.Duration) time.Duration { return capDelay },
	}
}

func TestWithRetry(t *testing.T) {
	base := RetryConfig{
		Enabled:        true,
		MaxAttempts:    3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     time.Second,
		RetryAfterMax:  30 * time.Second,
	}
	retryAfter7s := func(err error) retryDecision {
		if errors.Is(err, errRetryTransient) {
			return retryDecision{retry: true, reason: "transient", retryAfter: 7 * time.Second}
		}
		return retryDecision{}
	}

	tests := []struct {
		name      string
		retry     RetryConfig
		classify  func(error) retryDecision
		script    []error
		wantErr   error
		wantCalls int
		wantSleep []time.Duration
		wantInfos []retryAttemptInfo
	}{
		{
			name:      "success on first attempt does not retry",
			retry:     base,
			classify:  classifyRetryTransient,
			script:    []error{nil},
			wantCalls: 1,
		},
		{
			name:      "retries transient error until success with exponential backoff",
			retry:     base,
			classify:  classifyRetryTransient,
			script:    []error{errRetryTransient, errRetryTransient, nil},
			wantCalls: 3,
			wantSleep: []time.Duration{10 * time.Millisecond, 20 * time.Millisecond},
			wantInfos: []retryAttemptInfo{
				{Attempt: 2, MaxAttempts: 3, Delay: 10 * time.Millisecond, Reason: "transient"},
				{Attempt: 3, MaxAttempts: 3, Delay: 20 * time.Millisecond, Reason: "transient"},
			},
		},
		{
			name:      "stops at MaxAttempts and returns the last error",
			retry:     base,
			classify:  classifyRetryTransient,
			script:    []error{errRetryTransient, errRetryTransient, errRetryTransient},
			wantErr:   errRetryTransient,
			wantCalls: 3,
			wantSleep: []time.Duration{10 * time.Millisecond, 20 * time.Millisecond},
			wantInfos: []retryAttemptInfo{
				{Attempt: 2, MaxAttempts: 3, Delay: 10 * time.Millisecond, Reason: "transient"},
				{Attempt: 3, MaxAttempts: 3, Delay: 20 * time.Millisecond, Reason: "transient"},
			},
		},
		{
			name:      "retry disabled makes a single attempt",
			retry:     RetryConfig{Enabled: false, MaxAttempts: 3, InitialBackoff: 10 * time.Millisecond},
			classify:  classifyRetryTransient,
			script:    []error{errRetryTransient, nil},
			wantErr:   errRetryTransient,
			wantCalls: 1,
		},
		{
			name:      "zero MaxAttempts makes a single attempt",
			retry:     RetryConfig{Enabled: true, MaxAttempts: 0, InitialBackoff: 10 * time.Millisecond},
			classify:  classifyRetryTransient,
			script:    []error{errRetryTransient, nil},
			wantErr:   errRetryTransient,
			wantCalls: 1,
		},
		{
			name:      "non-retryable error returns immediately",
			retry:     base,
			classify:  classifyRetryTransient,
			script:    []error{errRetryFatal, nil},
			wantErr:   errRetryFatal,
			wantCalls: 1,
		},
		{
			name:      "retry-after overrides backoff",
			retry:     base,
			classify:  retryAfter7s,
			script:    []error{errRetryTransient, nil},
			wantCalls: 2,
			wantSleep: []time.Duration{7 * time.Second},
			wantInfos: []retryAttemptInfo{
				{Attempt: 2, MaxAttempts: 3, Delay: 7 * time.Second, Reason: "transient"},
			},
		},
		{
			name:      "backoff is capped at MaxBackoff",
			retry:     RetryConfig{Enabled: true, MaxAttempts: 4, InitialBackoff: 10 * time.Millisecond, MaxBackoff: 25 * time.Millisecond},
			classify:  classifyRetryTransient,
			script:    []error{errRetryTransient, errRetryTransient, errRetryTransient, nil},
			wantCalls: 4,
			wantSleep: []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 25 * time.Millisecond},
			wantInfos: []retryAttemptInfo{
				{Attempt: 2, MaxAttempts: 4, Delay: 10 * time.Millisecond, Reason: "transient"},
				{Attempt: 3, MaxAttempts: 4, Delay: 20 * time.Millisecond, Reason: "transient"},
				{Attempt: 4, MaxAttempts: 4, Delay: 25 * time.Millisecond, Reason: "transient"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				calls  int
				sleeps []time.Duration
				infos  []retryAttemptInfo
			)
			client := newRetryTestClient(tt.retry, func(_ context.Context, delay time.Duration) error {
				sleeps = append(sleeps, delay)
				return nil
			})

			err := client.withRetry(
				context.Background(),
				func(attempt int) (bool, error) {
					calls++
					if attempt != calls || calls > len(tt.script) {
						t.Fatalf("operation called with attempt %d (call %d), script has %d results", attempt, calls, len(tt.script))
					}
					return false, tt.script[calls-1]
				},
				tt.classify,
				func(info retryAttemptInfo) { infos = append(infos, info) },
			)

			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("withRetry() error = %v, want nil", err)
				}
			} else if !errors.Is(err, tt.wantErr) {
				t.Errorf("withRetry() error = %v, want %v", err, tt.wantErr)
			}
			if calls != tt.wantCalls {
				t.Errorf("operation calls = %d, want %d", calls, tt.wantCalls)
			}
			if !reflect.DeepEqual(sleeps, tt.wantSleep) {
				t.Errorf("sleeps = %v, want %v", sleeps, tt.wantSleep)
			}
			if !reflect.DeepEqual(infos, tt.wantInfos) {
				t.Errorf("retry infos = %+v, want %+v", infos, tt.wantInfos)
			}
		})
	}
}

func TestWithRetryNilClient(t *testing.T) {
	var client *Client
	calls := 0
	err := client.withRetry(
		context.Background(),
		func(int) (bool, error) {
			calls++
			return false, nil
		},
		classifyRetryTransient,
		nil,
	)
	if err == nil {
		t.Error("withRetry() on nil client error = nil, want error")
	}
	if calls != 0 {
		t.Errorf("operation calls = %d, want 0", calls)
	}
}

func TestWithRetryContextCancellation(t *testing.T) {
	t.Run("cancelled by operation skips backoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var sleeps []time.Duration
		client := newRetryTestClient(
			RetryConfig{Enabled: true, MaxAttempts: 3, InitialBackoff: 10 * time.Millisecond},
			func(_ context.Context, delay time.Duration) error {
				sleeps = append(sleeps, delay)
				return nil
			},
		)

		calls := 0
		err := client.withRetry(
			ctx,
			func(int) (bool, error) {
				calls++
				cancel()
				return false, errRetryTransient
			},
			classifyRetryTransient,
			nil,
		)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("withRetry() error = %v, want context.Canceled", err)
		}
		if calls != 1 {
			t.Errorf("operation calls = %d, want 1", calls)
		}
		if len(sleeps) != 0 {
			t.Errorf("sleeps = %v, want none after cancellation", sleeps)
		}
	})

	t.Run("cancelled during default sleep returns promptly", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		client := newRetryTestClient(
			RetryConfig{Enabled: true, MaxAttempts: 3, InitialBackoff: 10 * time.Millisecond},
			nil,
		)
		classifyLongRetryAfter := func(err error) retryDecision {
			return retryDecision{retry: true, reason: "transient", retryAfter: time.Hour}
		}

		calls := 0
		start := time.Now()
		err := client.withRetry(
			ctx,
			func(int) (bool, error) {
				calls++
				return false, errRetryTransient
			},
			classifyLongRetryAfter,
			nil,
		)
		elapsed := time.Since(start)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("withRetry() error = %v, want context.DeadlineExceeded", err)
		}
		if calls != 1 {
			t.Errorf("operation calls = %d, want 1", calls)
		}
		if elapsed > 5*time.Second {
			t.Errorf("withRetry() took %v after cancellation, want prompt return", elapsed)
		}
	})
}

func TestSleepForRetry(t *testing.T) {
	tests := []struct {
		name      string
		delay     time.Duration
		sleepErr  error
		wantErr   error
		wantSleep []time.Duration
	}{
		{name: "zero delay skips sleeper", delay: 0},
		{name: "negative delay skips sleeper", delay: -time.Second},
		{name: "positive delay calls sleeper", delay: 5 * time.Millisecond, wantSleep: []time.Duration{5 * time.Millisecond}},
		{
			name:      "sleeper error is returned",
			delay:     5 * time.Millisecond,
			sleepErr:  errRetryFatal,
			wantErr:   errRetryFatal,
			wantSleep: []time.Duration{5 * time.Millisecond},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sleeps []time.Duration
			client := newRetryTestClient(RetryConfig{}, func(_ context.Context, delay time.Duration) error {
				sleeps = append(sleeps, delay)
				return tt.sleepErr
			})

			err := client.sleepForRetry(context.Background(), tt.delay)
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("sleepForRetry() error = %v, want nil", err)
				}
			} else if !errors.Is(err, tt.wantErr) {
				t.Errorf("sleepForRetry() error = %v, want %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(sleeps, tt.wantSleep) {
				t.Errorf("sleeps = %v, want %v", sleeps, tt.wantSleep)
			}
		})
	}
}

func TestSleepForRetryNilClientUsesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var client *Client
	if err := client.sleepForRetry(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("sleepForRetry() on nil client error = %v, want context.Canceled", err)
	}
}
