package modelcatalog

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	// RefreshStatusFreshSkipped marks a provider whose cache is still fresh.
	RefreshStatusFreshSkipped = "fresh-skipped"
	// RefreshStatusUpdated marks a provider whose cache was refreshed.
	RefreshStatusUpdated = "updated"
	// RefreshStatusFailed marks a provider whose refresh failed.
	RefreshStatusFailed = "failed"
)

// RefreshOptions controls a provider model refresh sweep.
type RefreshOptions struct {
	Force    bool
	OnResult func(alias string, err error)
}

// RefreshResult is the outcome for one provider in a refresh sweep.
type RefreshResult struct {
	Alias  string
	Status string
	Err    error
}

// RefreshReport contains one result for each endpoint passed to RefreshAll.
type RefreshReport struct {
	Results []RefreshResult
}

// RefreshAll refreshes provider model caches with bounded concurrency. A
// provider failure is recorded in the report and does not stop the sweep.
func (s *Service) RefreshAll(ctx context.Context, endpoints []Endpoint, opts RefreshOptions) RefreshReport {
	report := RefreshReport{Results: make([]RefreshResult, len(endpoints))}
	if !s.DiscoveryEnabled {
		for i, endpoint := range endpoints {
			report.Results[i] = RefreshResult{Alias: endpoint.Alias, Status: RefreshStatusFreshSkipped}
			if opts.OnResult != nil {
				opts.OnResult(endpoint.Alias, nil)
			}
		}
		return report
	}

	jobs := make(chan int)
	var wait sync.WaitGroup
	if len(endpoints) == 0 {
		return report
	}
	workerCount := len(endpoints)
	if workerCount > 4 {
		workerCount = 4
	}
	for range workerCount {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				result := s.refreshOne(ctx, endpoints[index], opts.Force)
				report.Results[index] = result
				if opts.OnResult != nil {
					opts.OnResult(result.Alias, result.Err)
				}
			}
		}()
	}
	for index := range endpoints {
		jobs <- index
	}
	close(jobs)
	wait.Wait()
	return report
}

func (s *Service) refreshOne(parent context.Context, endpoint Endpoint, force bool) RefreshResult {
	result := RefreshResult{Alias: endpoint.Alias}
	if !force && s.cache.IsFresh(endpoint.Alias, endpoint.Type, endpoint.BaseURL) {
		result.Status = RefreshStatusFreshSkipped
		return result
	}

	etag := s.cachedETag(endpoint)
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	enumerator, err := s.dispatcher(endpoint.Type, s.client)
	if err != nil {
		return failedRefresh(result, fmt.Errorf("create model enumerator: %w", err))
	}
	refresh, err := enumerator.Enumerate(ctx, endpoint, EnumerationOptions{ETag: etag})
	if err != nil {
		return failedRefresh(result, fmt.Errorf("enumerate models for %s: %w", endpoint.Alias, err))
	}
	if refresh.NotModified {
		if !s.cache.ExtendFreshness(endpoint.Alias, refresh.ETag) {
			return failedRefresh(result, fmt.Errorf("extend model cache freshness for %s: etag did not match cached envelope", endpoint.Alias))
		}
		result.Status = RefreshStatusUpdated
		return result
	}
	if err := s.cache.SaveAtomic(endpoint.Alias, CacheEnvelope{
		Fingerprint: CacheFingerprint{ProviderType: endpoint.Type, BaseURL: endpoint.BaseURL},
		ETag:        refresh.ETag,
		Models:      refresh.Models,
	}); err != nil {
		return failedRefresh(result, fmt.Errorf("save model cache for %s: %w", endpoint.Alias, err))
	}
	result.Status = RefreshStatusUpdated
	return result
}

func (s *Service) cachedETag(endpoint Endpoint) string {
	if _, found, err := s.cache.Load(endpoint.Alias, endpoint.Type, endpoint.BaseURL); err != nil || !found {
		return ""
	}
	envelope, err := s.cache.readEnvelope(endpoint.Alias)
	if err != nil {
		return ""
	}
	return envelope.ETag
}

func failedRefresh(result RefreshResult, err error) RefreshResult {
	result.Status = RefreshStatusFailed
	result.Err = err
	return result
}

// DefaultDispatcher returns the built-in provider enumerator dispatcher.
func DefaultDispatcher(providerType string, client *http.Client) (Enumerator, error) {
	return ForTypeWithClient(providerType, client)
}
