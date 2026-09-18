package modelcatalog

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// TestRefreshAllSerializesOnResultDelivery proves provider enumeration stays
// parallel while the OnResult progress callback is delivered sequentially: a
// caller keeping shared state in OnResult would otherwise race.
func TestRefreshAllSerializesOnResultDelivery(t *testing.T) {
	const endpointCount = 8

	service := NewService(func(_ string, _ *http.Client) (Enumerator, error) {
		return testEnumerator{enumerate: func(_ context.Context, _ Endpoint, _ EnumerationOptions) (EnumerationResult, error) {
			time.Sleep(time.Millisecond)
			return EnumerationResult{Models: []DiscoveredModel{{ID: "model"}}}, nil
		}}, nil
	}, NewCache(t.TempDir()), NewStore(""), nil)

	endpoints := make([]Endpoint, endpointCount)
	for i := range endpoints {
		endpoints[i] = Endpoint{Alias: fmt.Sprintf("ep-%d", i), Type: "test", BaseURL: "http://example.invalid"}
	}

	var inFlight, maxInFlight atomic.Int64
	report := service.RefreshAll(context.Background(), endpoints, RefreshOptions{
		Force: true,
		OnResult: func(string, error) {
			current := inFlight.Add(1)
			for {
				peak := maxInFlight.Load()
				if current <= peak || maxInFlight.CompareAndSwap(peak, current) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			inFlight.Add(-1)
		},
	})

	if len(report.Results) != endpointCount {
		t.Fatalf("results = %d, want %d", len(report.Results), endpointCount)
	}
	if peak := maxInFlight.Load(); peak != 1 {
		t.Fatalf("max concurrent OnResult deliveries = %d, want 1", peak)
	}
}
