package modelcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// CodexCredentials supplies the OAuth access token and ChatGPT account ID for Codex enumeration.
type CodexCredentials func(context.Context) (accessToken, accountID string, err error)

// CodexEnumerator discovers models from the ChatGPT Codex endpoint.
type CodexEnumerator struct {
	client        *http.Client
	clientVersion string
	credentials   CodexCredentials
}

// NewCodexEnumerator creates a Codex model enumerator.
func NewCodexEnumerator(client *http.Client, clientVersion string, credentials CodexCredentials) *CodexEnumerator {
	return &CodexEnumerator{
		client:        clientOrDefault(client),
		clientVersion: clientVersion,
		credentials:   credentials,
	}
}

type codexModelsResponse struct {
	Models []codexModel `json:"models"`
}

type codexModel struct {
	Slug                     string                `json:"slug"`
	DisplayName              string                `json:"display_name"`
	Description              string                `json:"description"`
	Visibility               string                `json:"visibility"`
	ContextWindow            int                   `json:"context_window"`
	SupportedReasoningLevels []codexReasoningLevel `json:"supported_reasoning_levels"`
	Priority                 int                   `json:"priority"`
}

type codexReasoningLevel struct {
	Effort string `json:"effort"`
}

// Enumerate discovers visible models from Codex.
func (e *CodexEnumerator) Enumerate(ctx context.Context, ep Endpoint, opts EnumerationOptions) (EnumerationResult, error) {
	response, etag, notModified, err := e.requestModels(ctx, ep, opts)
	if err != nil {
		return EnumerationResult{}, err
	}
	if notModified {
		return EnumerationResult{ETag: etag, NotModified: true}, nil
	}
	return EnumerationResult{Models: codexModels(ep, response.Models), ETag: etag}, nil
}

func (e *CodexEnumerator) requestModels(ctx context.Context, ep Endpoint, opts EnumerationOptions) (codexModelsResponse, string, bool, error) {
	req, err := e.codexRequest(ctx, ep, opts)
	if err != nil {
		return codexModelsResponse{}, "", false, err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return codexModelsResponse{}, "", false, fmt.Errorf("request Codex models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // Response body cleanup errors do not change enumeration result.
	etag := resp.Header.Get("ETag")
	if resp.StatusCode == http.StatusNotModified {
		return codexModelsResponse{}, etag, true, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return codexModelsResponse{}, "", false, fmt.Errorf("enumerate Codex models: unexpected status code %d", resp.StatusCode)
	}
	response, err := decodeCodexModels(resp)
	if err != nil {
		return codexModelsResponse{}, "", false, err
	}
	return response, etag, false, nil
}

func (e *CodexEnumerator) codexRequest(ctx context.Context, ep Endpoint, opts EnumerationOptions) (*http.Request, error) {
	if e.credentials == nil {
		return nil, fmt.Errorf("enumerate Codex models: credentials callback is required")
	}
	accessToken, accountID, err := e.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("get Codex credentials: %w", err)
	}
	if accessToken == "" || accountID == "" {
		return nil, fmt.Errorf("enumerate Codex models: credentials are missing")
	}
	u, err := url.Parse(ep.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Codex base URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("parse Codex base URL: missing scheme or host")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/models"
	query := u.Query()
	query.Set("client_version", e.clientVersion)
	u.RawQuery = query.Encode()
	req, err := newGETRequest(ctx, ep, u.String(), bearerAuthorization(accessToken))
	if err != nil {
		return nil, err
	}
	req.Header.Set("ChatGPT-Account-ID", accountID)
	req.Header.Set("OAI-Product-Sku", "codex")
	if opts.ETag != "" {
		req.Header.Set("If-None-Match", opts.ETag)
	}
	return req, nil
}

func decodeCodexModels(resp *http.Response) (codexModelsResponse, error) {
	var response codexModelsResponse
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&response); err != nil {
		return codexModelsResponse{}, fmt.Errorf("decode Codex models response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return codexModelsResponse{}, fmt.Errorf("decode Codex models response: unexpected trailing JSON")
		}
		return codexModelsResponse{}, fmt.Errorf("decode Codex models response: %w", err)
	}
	return response, nil
}

func codexModels(ep Endpoint, items []codexModel) []DiscoveredModel {
	models := make([]DiscoveredModel, 0, len(items))
	for _, item := range items {
		if item.Visibility != "list" {
			continue
		}
		displayName := item.DisplayName
		if displayName == "" {
			displayName = item.Slug
		}
		efforts := make([]string, 0, len(item.SupportedReasoningLevels))
		for _, level := range item.SupportedReasoningLevels {
			efforts = append(efforts, level.Effort)
		}
		models = append(models, DiscoveredModel{
			ProviderAlias:    ep.Alias,
			ProviderType:     ep.Type,
			ID:               item.Slug,
			DisplayName:      displayName,
			Description:      item.Description,
			ContextLength:    item.ContextWindow,
			SupportedEfforts: efforts,
			Priority:         item.Priority,
		})
	}
	return models
}
