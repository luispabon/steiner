package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

// TestReportCompactionFailure proves a failed operation emits one skipped failure
// event and is marked for the manual path. Cancellation and a missing sink emit
// nothing and stay unmarked.
func TestReportCompactionFailure(t *testing.T) {
	t.Parallel()
	boom := errors.New("provider down")
	tests := []struct {
		name       string
		withSink   bool
		err        error
		wantEvents int
		wantMarked bool
	}{
		{name: "provider error is reported and marked", withSink: true, err: boom, wantEvents: 1, wantMarked: true},
		{name: "empty summary is reported and marked", withSink: true, err: &compactionSummaryError{Reason: summaryFailNoText, Attempts: 3}, wantEvents: 1, wantMarked: true},
		{name: "cancellation is left to the caller", withSink: true, err: context.Canceled, wantEvents: 0},
		{name: "no sink emits nothing and is not marked", err: boom, wantEvents: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var rec eventRecorder
			var sink output.EventSink
			if tt.withSink {
				sink = rec.sink()
			}
			reported := reportCompactionFailure(sink, 2, nil, tt.err)
			if got := len(rec.snapshot()); got != tt.wantEvents {
				t.Errorf("events = %d, want %d", got, tt.wantEvents)
			}
			if got := CompactionFailureReported(reported); got != tt.wantMarked {
				t.Errorf("CompactionFailureReported() = %t, want %t", got, tt.wantMarked)
			}
			if !errors.Is(reported, tt.err) {
				t.Errorf("reported error = %v, want it to wrap %v", reported, tt.err)
			}
		})
	}
}

// TestCompactionFailureReportsEmptySummaryAttempts proves the failure event of an
// empty summary carries its attempts and reason, from a real stage failure.
func TestCompactionFailureReportsEmptySummaryAttempts(t *testing.T) {
	t.Parallel()
	r := retryRun(retryEmptyReply(), retryEmptyReply(), retryEmptyReply())
	_, err := r.compact(context.Background())
	var rec eventRecorder
	reported := reportCompactionFailure(rec.sink(), 5, nil, err)
	if !CompactionFailureReported(reported) {
		t.Fatalf("CompactionFailureReported(%v) = false, want true", reported)
	}
	events := rec.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want exactly one failure event", len(events))
	}
	payload, ok := output.AsContextCompactionEvent(events[0].Payload)
	if !ok {
		t.Fatalf("payload type = %T, want output.ContextCompactionEvent", events[0].Payload)
	}
	if payload.Severity != "skipped" || payload.CompactionResult != output.CompactionResultFailed {
		t.Errorf("Severity, CompactionResult = %q, %q, want skipped, failed", payload.Severity, payload.CompactionResult)
	}
	if payload.SummaryAttempts != 3 || payload.SummaryFailureReason != summaryFailNoText {
		t.Errorf("SummaryAttempts, SummaryFailureReason = %d, %q, want 3, %q", payload.SummaryAttempts, payload.SummaryFailureReason, summaryFailNoText)
	}
}

// TestCompactionSummaryErrorIsNotRetriedByRunLoop proves the empty-summary error,
// alone or reported, matches neither the transient provider retry nor
// RetryableProviderError, so the run loop does not retry the turn.
func TestCompactionSummaryErrorIsNotRetriedByRunLoop(t *testing.T) {
	t.Parallel()
	summaryErr := &compactionSummaryError{Reason: summaryFailNoText, Attempts: 3}
	for _, err := range []error{summaryErr, reportCompactionFailure(output.NoopSink{}, 1, nil, summaryErr)} {
		retries := 0
		shouldRetry, sleepErr := handleTransientProviderRetry(context.Background(), output.NoopSink{}, 1, err, &retries)
		if shouldRetry || sleepErr != nil || retries != 0 {
			t.Errorf("handleTransientProviderRetry(%v) = %t, %v, retries %d, want no retry", err, shouldRetry, sleepErr, retries)
		}
		if _, ok := provider.RetryableProviderError(err); ok {
			t.Errorf("RetryableProviderError(%v) matched, want no match", err)
		}
	}
}
