// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
	"sync"
)

// cancelRegistry maps an in-flight request's id to the cancel func of
// its per-request context, so a specific running request can be
// cancelled by id. It is the addressable face of the per-request cancel
// handle that already exists inside handle: the registry stores that
// same func — it does not mint a second cancellation authority. The two
// cancel authorities remain (1) session shutdown, which cancels the
// parent serverCtx and so every derived per-request context at once, and
// (2) per-request cancel on completion, the child handle handle releases
// via defer when its handler returns. The registry is a second address
// for that child handle, not a third authority.
//
// The per-request context is a plain cancellable chain derived from
// serverCtx, so a deadline composes onto it by construction
// (context.WithDeadline / WithTimeout layers on with no structural
// change); no deadline is set today.
//
// Wiring status: the registry tracks every id-bearing request for its
// in-flight lifetime and exposes a per-id cancel pickup, but no method,
// notification routing, or other production caller fires that pickup
// today. The handle by which a future per-request cancel-by-id consumer
// would reach a running request is present and exercised by tests;
// routing such a consumer's notification to it is not yet wired, so
// nothing cancels a specific in-flight request by id at runtime. The
// registry is populated for lifecycle correctness (balanced track /
// untrack under the pool) and stands ready for that wiring.
type cancelRegistry struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// newCancelRegistry returns an empty registry ready for concurrent
// track / untrack from the worker pool.
func newCancelRegistry() *cancelRegistry {
	return &cancelRegistry{cancels: map[string]context.CancelFunc{}}
}

// track records cancel under the request's id for the duration of its
// handling. id is the raw-JSON id bytes the request carried — the same
// correlation identity the dispatch path keys on. An empty id (a
// notification, which carries no id and cannot be cancelled) is not
// tracked. The single-client daemon model makes a duplicate live id
// unexpected; should one occur, last-writer-wins — the later handler's
// cancel replaces the earlier under the same key.
func (r *cancelRegistry) track(id json.RawMessage, cancel context.CancelFunc) {
	if len(id) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancels[string(id)] = cancel
}

// untrack removes the registry entry for id. Called when a handler
// returns (paired with track), so the registry holds only the currently
// in-flight requests. Removing an absent id is a no-op.
func (r *cancelRegistry) untrack(id json.RawMessage) {
	if len(id) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cancels, string(id))
}

// cancel looks up id's registered cancel func and calls it, cancelling
// that request's per-request context so its handler observes ctx.Err()
// at its next interruption point and returns. Cancelling an id that is
// not currently tracked is a safe no-op.
//
// This is the pickup point a future per-request cancel-by-id consumer
// would call; no production caller fires it today (see cancelRegistry).
func (r *cancelRegistry) cancel(id json.RawMessage) {
	if len(id) == 0 {
		return
	}
	r.mu.Lock()
	c, ok := r.cancels[string(id)]
	r.mu.Unlock()
	if ok {
		c()
	}
}

// len reports the number of currently-tracked in-flight requests. Tests
// assert it returns to zero after a burst drains, proving track /
// untrack stay balanced under the pool.
func (r *cancelRegistry) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cancels)
}
