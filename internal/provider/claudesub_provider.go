package provider

import (
	"context"
	"errors"
	"strings"
)

// ClaudeSubscriptionProvider is the stateful provider facade for a signed-in
// Claude Code subscription. The pool owns process lifetime; the provider owns
// request routing and turn orchestration.
type ClaudeSubscriptionProvider struct {
	pool *ClaudeSubscriptionPool
	// streamErrorLog receives one provider-stream diagnostics record per turn.
	// Nil disables it.
	streamErrorLog *StreamErrorLogger
}

// NewClaudeSubscriptionProvider creates a provider backed by pool. Each turn
// writes one provider-stream diagnostics record to streamErrorLog when set.
func NewClaudeSubscriptionProvider(pool *ClaudeSubscriptionPool, streamErrorLog *StreamErrorLogger) *ClaudeSubscriptionProvider {
	return &ClaudeSubscriptionProvider{pool: pool, streamErrorLog: streamErrorLog}
}

// SupportsUsageStats reports that Claude subscription turns expose usage.
func (p *ClaudeSubscriptionProvider) SupportsUsageStats() bool { return true }

// StatefulTranscript reports that the CLI owns the conversation transcript.
func (p *ClaudeSubscriptionProvider) StatefulTranscript() bool { return true }

// SupportsStatefulCompaction reports that compaction summaries follow the
// claude_subscription transition contract (see claudeSubSummaryTurn).
func (p *ClaudeSubscriptionProvider) SupportsStatefulCompaction() bool { return true }

// ChatCompletion folds the streamed turn into one response.
func (p *ClaudeSubscriptionProvider) ChatCompletion(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	stream, err := p.StreamChatCompletion(ctx, req)
	if err != nil {
		return ChatResponse{}, err
	}
	var chunks []ChatChunk
	for chunk := range stream {
		chunks = append(chunks, chunk)
	}
	response, err := foldClaudeSubChunks(chunks)
	if err != nil && ctx.Err() != nil && !claudeSubChunksHaveDone(chunks) {
		return ChatResponse{}, ctx.Err()
	}
	return response, err
}

func claudeSubChunksHaveDone(chunks []ChatChunk) bool {
	for _, chunk := range chunks {
		if chunk.Done {
			return true
		}
	}
	return false
}

func foldClaudeSubChunks(chunks []ChatChunk) (ChatResponse, error) {
	var (
		response  ChatResponse
		content   strings.Builder
		reasoning strings.Builder
	)
	terminal := false
	for _, chunk := range chunks {
		if chunk.Error != "" {
			if chunk.OriginalError != nil {
				return ChatResponse{}, chunk.OriginalError
			}
			return ChatResponse{}, &claudeSubTurnError{message: chunk.Error}
		}
		if chunk.Done {
			terminal = true
		}
		if chunk.Delta.Role == MessageRoleAssistant {
			response.Message.Role = MessageRoleAssistant
		}
		if chunk.ContentSnapshot {
			content.Reset()
		}
		content.WriteString(chunk.Delta.Content)
		reasoning.WriteString(chunk.Thinking)
		if chunk.Delta.ReasoningContent != "" && reasoning.Len() == 0 {
			reasoning.WriteString(chunk.Delta.ReasoningContent)
		}
		if len(chunk.Delta.ToolCalls) > 0 {
			response.Message.ToolCalls = append(response.Message.ToolCalls, chunk.Delta.ToolCalls...)
		}
		if chunk.Usage != nil {
			response.Usage = chunk.Usage
		}
		if chunk.FinishReason != "" {
			response.FinishReason = chunk.FinishReason
		}
		if chunk.BackendState != nil {
			response.BackendState = chunk.BackendState
		}
		if chunk.ClearBackendState {
			response.ClearBackendState = true
		}
	}
	if !terminal {
		return ChatResponse{}, errors.New("claude_subscription stream closed without a terminal chunk")
	}
	response.Message.Content = content.String()
	response.Message.ReasoningContent = reasoning.String()
	return response, nil
}

// StreamChatCompletion starts one stateful Claude subscription turn.
func (p *ClaudeSubscriptionProvider) StreamChatCompletion(ctx context.Context, req ChatRequest) (<-chan ChatChunk, error) {
	if p == nil || p.pool == nil {
		return nil, &claudeSubTurnError{message: "claude_subscription provider is not initialized"}
	}
	return claudeSubStream(ctx, p.pool, req, p.streamErrorLog)
}

type claudeSubTurnError struct{ message string }

func (e *claudeSubTurnError) Error() string { return e.message }
