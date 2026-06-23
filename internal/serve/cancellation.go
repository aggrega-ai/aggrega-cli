// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
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
// The registry tracks every id-bearing request for its in-flight
// lifetime and exposes a per-id cancel pickup. The pickup is wired to
// the $/cancelRequest notification: the dispatch loop routes that
// notification's id to cancel, so a client cancels a specific in-flight
// request by id at runtime. The registry remains a second address for
// the per-request cancel handle, not a second cancellation authority.
type cancelRegistry struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// newCancelRegistry returns an empty registry ready for concurrent
// track / untrack from the worker pool.
func newCancelRegistry() *cancelRegistry {
	return &cancelRegistry{cancels: map[string]context.CancelFunc{}}
}

// cancelParams is the params shape for the $/cancelRequest notification.
// ID is captured as raw JSON — not decoded to a concrete int/string —
// so it round-trips to the registry's key through the same cancelKey
// canonicalization the in-flight request's id took, with no
// decode-then-re-encode step that could perturb the bytes.
type cancelParams struct {
	ID json.RawMessage `json:"id"`
}

// cancelKey canonicalizes a raw-JSON id into the registry's map key.
// The id reaches the registry from two wire surfaces — the request's
// own id (track / untrack) and the $/cancelRequest notification's
// params.id (cancel) — which a client may JSON-encode with different
// insignificant whitespace for the same logical id (`7` vs ` 7`,
// `"a"` vs `"a" `). json.Compact removes that whitespace so both
// surfaces resolve to one key and a cancel never silently misses an
// in-flight request over a formatting difference. A compaction error
// (malformed JSON, which a parsed id should never be) falls back to
// the raw bytes so the key is always defined.
func cancelKey(id json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, id); err != nil {
		return string(id)
	}
	return buf.String()
}

// track records cancel under the request's id for the duration of its
// handling. id is the raw-JSON id bytes the request carried, canonicalized
// by cancelKey so it resolves to the same key as the $/cancelRequest
// pickup. An empty id (a notification, which carries no id and cannot be
// cancelled) is not tracked. The single-client daemon model makes a
// duplicate live id unexpected; should one occur, last-writer-wins — the
// later handler's cancel replaces the earlier under the same key.
func (r *cancelRegistry) track(id json.RawMessage, cancel context.CancelFunc) {
	if len(id) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancels[cancelKey(id)] = cancel
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
	delete(r.cancels, cancelKey(id))
}

// cancel looks up id's registered cancel func and calls it, cancelling
// that request's per-request context so its handler observes ctx.Err()
// at its next interruption point and returns. Cancelling an id that is
// not currently tracked is a safe no-op.
//
// This is the pickup point the $/cancelRequest dispatch case calls,
// passing the notification's params.id (see cancelRegistry).
func (r *cancelRegistry) cancel(id json.RawMessage) {
	if len(id) == 0 {
		return
	}
	r.mu.Lock()
	c, ok := r.cancels[cancelKey(id)]
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
