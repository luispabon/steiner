package provider

import (
	"testing"
	"time"
)

func TestParseLiteLLMRetryAfter(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantDuration time.Duration
		wantOK       bool
	}{
		{
			name:         "text pattern with 11 seconds",
			body:         "Try again in 11 seconds",
			wantDuration: 11 * time.Second,
			wantOK:       true,
		},
		{
			name:         "text pattern with 120 seconds",
			body:         "Try again in 120 seconds",
			wantDuration: 120 * time.Second,
			wantOK:       true,
		},
		{
			name:         "text pattern in longer message",
			body:         "Rate limit exceeded. Try again in 30 seconds for better luck",
			wantDuration: 30 * time.Second,
			wantOK:       true,
		},
		{
			name:         "JSON pattern with retry_after",
			body:         `{"error": "rate limit", "retry_after": 30}`,
			wantDuration: 30 * time.Second,
			wantOK:       true,
		},
		{
			name:         "JSON pattern with retry_after and whitespace",
			body:         `{"error": "rate limit", "retry_after":   45}`,
			wantDuration: 45 * time.Second,
			wantOK:       true,
		},
		{
			name:         "empty string",
			body:         "",
			wantDuration: 0,
			wantOK:       false,
		},
		{
			name:         "unrelated error text",
			body:         "An error occurred while processing your request",
			wantDuration: 0,
			wantOK:       false,
		},
		{
			name:         "budget exceeded error",
			body:         "Budget has been exceeded!",
			wantDuration: 0,
			wantOK:       false,
		},
		{
			name:         "text pattern at word boundary",
			body:         "Please Try again in 15 seconds tomorrow",
			wantDuration: 15 * time.Second,
			wantOK:       true,
		},
		{
			name:         "malformed JSON with non-numeric retry_after",
			body:         `{"retry_after": "notanumber"}`,
			wantDuration: 0,
			wantOK:       false,
		},
		{
			name:         "text pattern with zero seconds",
			body:         "Try again in 0 seconds",
			wantDuration: 0 * time.Second,
			wantOK:       true,
		},
		{
			name:         "both patterns present, text matches first",
			body:         `Try again in 25 seconds and {"retry_after": 50}`,
			wantDuration: 25 * time.Second,
			wantOK:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseLiteLLMRetryAfter(tt.body)
			if ok != tt.wantOK {
				t.Errorf("parseLiteLLMRetryAfter() ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.wantDuration {
				t.Errorf("parseLiteLLMRetryAfter() got %v, want %v", got, tt.wantDuration)
			}
		})
	}
}

func TestIsLiteLLMBudgetExceeded(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "budget exceeded exact case",
			body: "Budget has been exceeded",
			want: true,
		},
		{
			name: "budget exceeded with punctuation",
			body: "Budget has been exceeded!",
			want: true,
		},
		{
			name: "budget exceeded lowercase",
			body: "budget has been exceeded",
			want: true,
		},
		{
			name: "budget exceeded mixed case",
			body: "BudGet has been eXceeded",
			want: true,
		},
		{
			name: "budget exceeded in longer message",
			body: "Error: Budget has been exceeded. Please top up your account.",
			want: true,
		},
		{
			name: "regular 429 error",
			body: `{"error": "Too Many Requests", "status": 429}`,
			want: false,
		},
		{
			name: "rate limit error",
			body: "Rate limit exceeded. Try again in 30 seconds",
			want: false,
		},
		{
			name: "empty string",
			body: "",
			want: false,
		},
		{
			name: "similar but not exact match",
			body: "Your budget was exceeded",
			want: false,
		},
		{
			name: "try again message without budget",
			body: "Try again in 60 seconds",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLiteLLMBudgetExceeded(tt.body)
			if got != tt.want {
				t.Errorf("isLiteLLMBudgetExceeded() got %v, want %v", got, tt.want)
			}
		})
	}
}
