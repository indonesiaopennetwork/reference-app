// Package discoverwait bridges a genuinely async Beckn on_discover callback
// back to the synchronous HTTP request that triggered it, so bap-application
// can keep presenting a plain synchronous GET /api/v1/discover to its own
// frontend while actually using the spec-correct async POST /discover +
// on_discover contract underneath against discovery-service — rather than
// discovery-service's separate GET /discover sync shortcut. See
// clientsvc.ClientService.Discover (the waiter) and
// on_discover_svc.OnDiscoverService.Process (the deliverer), the two
// callers on either side of this package.
package discoverwait

import (
	"encoding/json"
	"sync"
)

// Registry correlates a discover transactionId with the channel a waiting
// HTTP request goroutine blocks on, and the on_discover receiver delivers
// the result to once discovery-service's callback actually arrives.
type Registry struct {
	mu      sync.Mutex
	waiters map[string]chan json.RawMessage
}

// NewRegistry returns an empty Registry, safe for concurrent use.
func NewRegistry() *Registry {
	return &Registry{waiters: make(map[string]chan json.RawMessage)}
}

// Register creates a buffered (capacity 1) channel for transactionID and
// returns it. Call this BEFORE sending the outbound /discover request, so
// a callback that arrives unusually fast can never race ahead of the
// waiter being registered. The caller must eventually call Cancel exactly
// once (success or timeout) to release the entry.
func (r *Registry) Register(transactionID string) <-chan json.RawMessage {
	ch := make(chan json.RawMessage, 1)
	r.mu.Lock()
	r.waiters[transactionID] = ch
	r.mu.Unlock()
	return ch
}

// Cancel removes transactionID's waiter, if one is still registered. Safe
// to call after the channel has already received a value (Deliver already
// removed it — this is then a no-op) or if it never will.
func (r *Registry) Cancel(transactionID string) {
	r.mu.Lock()
	delete(r.waiters, transactionID)
	r.mu.Unlock()
}

// Deliver sends payload to transactionID's waiter, if one is currently
// registered, and reports whether a waiter was found. false means either
// nothing ever called Register for this transactionId (an unsolicited or
// stale callback) or the waiter already gave up and called Cancel (timed
// out) — both are ordinary, loggable-but-not-fatal conditions for the
// caller, not an error in this package.
func (r *Registry) Deliver(transactionID string, payload json.RawMessage) bool {
	r.mu.Lock()
	ch, ok := r.waiters[transactionID]
	if ok {
		delete(r.waiters, transactionID)
	}
	r.mu.Unlock()

	if !ok {
		return false
	}
	ch <- payload
	return true
}
