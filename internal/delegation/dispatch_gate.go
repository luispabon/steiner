package delegation

import (
	"context"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

// applyDispatchGate coordinates same-cache-key sibling delegations so concurrent
// same-AgentType children do not race each other over a provider cache that is only
// warm once a leader has populated it. It returns an events sink and release function.
func applyDispatchGate(ctx context.Context, store *CacheKeyStore, cacheKey string, occ output.DelegationOccurrence, parentEvents, childEvents output.EventSink) (output.EventSink, func()) {
	if store == nil {
		return childEvents, func() {}
	}
	isLeader, release, wait := store.BeginDispatch(cacheKey)
	if isLeader {
		return newDispatchReleaseSink(childEvents, release), release
	}
	start := time.Now()
	if parentEvents != nil {
		parentEvents.Emit(output.NewDelegationCacheWaitingEvent(occ, start.Add(dispatchGateTimeout)))
	}
	reason := wait(ctx)
	if parentEvents != nil {
		parentEvents.Emit(output.NewDelegationCacheReleasedEvent(occ, reason, time.Since(start).Milliseconds()))
	}
	return childEvents, func() {}
}
