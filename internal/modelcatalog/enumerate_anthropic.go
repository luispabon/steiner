package modelcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const (
	anthropicVersion       = "2023-06-01"
	anthropicLargePageSize = 1000
	anthropicSmallPageSize = 20
)

// AnthropicEnumerator discovers models from Anthropic's models endpoint.
type AnthropicEnumerator struct {
	client *http.Client
}

// NewAnthropicEnumerator creates an Anthropic model enumerator.
func NewAnthropicEnumerator(client *http.Client) *AnthropicEnumerator {
	return &AnthropicEnumerator{client: clientOrDefault(client)}
}

type anthropicListResponse struct {
	Data    []anthropicModel `json:"data"`
	HasMore bool             `json:"has_more"`
	LastID  string           `json:"last_id"`
}

type anthropicModel struct {
	ID             string                `json:"id"`
	DisplayName    string                `json:"display_name"`
	MaxInputTokens int                   `json:"max_input_tokens"`
	Capabilities   anthropicCapabilities `json:"capabilities"`
}

type anthropicCapabilities struct {
	Effort map[string]anthropicCapability `json:"effort"`
}

type anthropicCapability struct {
	Supported bool `json:"supported"`
}

// Enumerate discovers models from Anthropic.
func (e *AnthropicEnumerator) Enumerate(ctx context.Context, ep Endpoint, _ EnumerationOptions) (EnumerationResult, error) {
	endpoint, err := joinModelsURL("anthropic", ep.BaseURL)
	if err != nil {
		return EnumerationResult{}, err
	}
	models, etag, err := e.enumeratePages(ctx, ep, endpoint, anthropicLargePageSize)
	if err == nil {
		return EnumerationResult{Models: models, ETag: etag}, nil
	}
	if !isAnthropicPageSizeError(err) {
		return EnumerationResult{}, err
	}
	models, etag, err = e.enumeratePages(ctx, ep, endpoint, anthropicSmallPageSize)
	if err != nil {
		return EnumerationResult{}, err
	}
	return EnumerationResult{Models: models, ETag: etag}, nil
}

func (e *AnthropicEnumerator) enumeratePages(ctx context.Context, ep Endpoint, endpoint string, pageSize int) ([]DiscoveredModel, string, error) {
	models := make([]DiscoveredModel, 0)
	cursor := ""
	var etag string
	for {
		response, pageETag, status, err := e.enumeratePage(ctx, ep, endpoint, pageSize, cursor)
		if err != nil {
			if status == http.StatusBadRequest && pageSize == anthropicLargePageSize && isAnthropicPageSizeError(err) {
				return nil, "", err
			}
			return nil, "", err
		}
		if status != http.StatusOK {
			return nil, "", fmt.Errorf("enumerate models: unexpected status code %d", status)
		}
		if pageETag != "" {
			etag = pageETag
		}
		models = append(models, anthropicModels(ep, response.Data)...)
		if !response.HasMore || response.LastID == "" {
			return models, etag, nil
		}
		cursor = response.LastID
	}
}

func (e *AnthropicEnumerator) enumeratePage(ctx context.Context, ep Endpoint, endpoint string, pageSize int, cursor string) (anthropicListResponse, string, int, error) {
	pageURL, err := anthropicPageURL(endpoint, pageSize, cursor)
	if err != nil {
		return anthropicListResponse{}, "", 0, err
	}
	req, err := newGETRequest(ctx, ep, pageURL, anthropicAuthorization(ep.APIKey), strings.HasPrefix(ep.APIKey, "Bearer "))
	if err != nil {
		return anthropicListResponse{}, "", 0, err
	}
	req.Header.Set("anthropic-version", anthropicVersion)
	if ep.APIKey != "" && !strings.HasPrefix(ep.APIKey, "Bearer ") {
		req.Header.Set("x-api-key", ep.APIKey)
	}
	return doAnthropicRequest(e.client, req)
}

func anthropicModels(ep Endpoint, items []anthropicModel) []DiscoveredModel {
	models := make([]DiscoveredModel, 0, len(items))
	for _, item := range items {
		displayName := item.DisplayName
		if displayName == "" {
			displayName = item.ID
		}
		efforts := make([]string, 0, len(item.Capabilities.Effort))
		for effort, capability := range item.Capabilities.Effort {
			if capability.Supported {
				efforts = append(efforts, effort)
			}
		}
		sort.Strings(efforts)
		models = append(models, DiscoveredModel{
			ProviderAlias:    ep.Alias,
			ProviderType:     ep.Type,
			ID:               item.ID,
			DisplayName:      displayName,
			ContextLength:    item.MaxInputTokens,
			SupportedEfforts: efforts,
		})
	}
	return models
}

func anthropicPageURL(endpoint string, pageSize int, cursor string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse Anthropic models URL: %w", err)
	}
	query := u.Query()
	query.Set("limit", fmt.Sprint(pageSize))
	if cursor == "" {
		query.Del("after_id")
	} else {
		query.Set("after_id", cursor)
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func anthropicAuthorization(apiKey string) string {
	if strings.HasPrefix(apiKey, "Bearer ") {
		return apiKey
	}
	return ""
}

func isAnthropicPageSizeError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "anthropic page size rejected")
}

func doAnthropicRequest(client *http.Client, req *http.Request) (anthropicListResponse, string, int, error) {
	resp, err := client.Do(req)
	if err != nil {
		return anthropicListResponse{}, "", 0, fmt.Errorf("request model enumeration: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // Response body cleanup errors do not change enumeration result.
	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return anthropicListResponse{}, "", resp.StatusCode, fmt.Errorf("read Anthropic error response: %w", readErr)
		}
		message := string(body)
		if resp.StatusCode == http.StatusBadRequest && (strings.Contains(strings.ToLower(message), "limit") || strings.Contains(strings.ToLower(message), "page size")) {
			return anthropicListResponse{}, "", resp.StatusCode, fmt.Errorf("anthropic page size rejected: %s", message)
		}
		return anthropicListResponse{}, "", resp.StatusCode, fmt.Errorf("enumerate models: unexpected status code %d", resp.StatusCode)
	}
	var response anthropicListResponse
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&response); err != nil {
		return anthropicListResponse{}, "", resp.StatusCode, fmt.Errorf("decode model enumeration response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return anthropicListResponse{}, "", resp.StatusCode, fmt.Errorf("decode model enumeration response: unexpected trailing JSON")
		}
		return anthropicListResponse{}, "", resp.StatusCode, fmt.Errorf("decode model enumeration response: %w", err)
	}
	return response, resp.Header.Get("ETag"), resp.StatusCode, nil
}
