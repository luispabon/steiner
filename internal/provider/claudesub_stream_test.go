package provider

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClaudeSubStreamEmitsTerminalError(t *testing.T) {
	pool, _ := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := claudeSubStream(ctx, pool, ChatRequest{Model: "m", Messages: []Message{{Role: MessageRoleAssistant, Content: "invalid"}}})
	if err != nil {
		t.Fatalf("claudeSubStream() error = %v", err)
	}
	var chunk ChatChunk
	select {
	case chunk = <-stream:
	case <-ctx.Done():
		t.Fatal("stream did not emit terminal error")
	}
	if !chunk.Done || chunk.Error == "" || chunk.OriginalError == nil {
		t.Fatalf("terminal chunk = %+v, want done error with original error", chunk)
	}
}

func TestClaudeSubStreamCancellationStopsDelivery(t *testing.T) {
	pool, _ := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := claudeSubStream(ctx, pool, ChatRequest{Model: "m", Messages: []Message{{Role: MessageRoleAssistant, Content: "invalid"}}})
	if err != nil {
		t.Fatalf("claudeSubStream() error = %v", err)
	}
	cancel()
	for chunk := range stream {
		if chunk.OriginalError != nil && !errors.Is(chunk.OriginalError, context.Canceled) {
			t.Errorf("stream error = %v, want cancellation or no chunk", chunk.OriginalError)
		}
	}
}
