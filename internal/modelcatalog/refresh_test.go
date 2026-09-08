package modelcatalog

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestServiceRefreshPreparesEndpointBeforeCacheLookup(t *testing.T) {
	var prepared atomic.Bool
	var sawBase string
	service := NewService(func(_ string, _ *http.Client) (Enumerator, error) {
		return testEnumerator{enumerate: func(_ context.Context, endpoint Endpoint, _ EnumerationOptions) (EnumerationResult, error) {
			sawBase = endpoint.BaseURL
			return EnumerationResult{Models: []DiscoveredModel{{ID: "model"}}}, nil
		}}, nil
	}, NewCache(t.TempDir()), NewStore(""), nil)
	endpoint := Endpoint{Alias: "codex", Type: "codex", BaseURL: "stale", Prepare: func(context.Context) (Endpoint, error) {
		prepared.Store(true)
		return Endpoint{Alias: "codex", Type: "codex", BaseURL: "current"}, nil
	}}
	got := service.RefreshAll(context.Background(), []Endpoint{endpoint}, RefreshOptions{Force: true})
	if got.Results[0].Err != nil || !prepared.Load() || sawBase != "current" {
		t.Fatalf("refresh: report=%+v prepared=%v base=%q", got, prepared.Load(), sawBase)
	}
}
