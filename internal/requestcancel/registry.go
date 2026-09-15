// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package requestcancel holds the registry that makes one in-flight request
// addressable by its id, so a client can cancel that request and leave every
// other one running.
//
// It is shared rather than per-frontend. Both request-serving frontends need the
// same mechanic — a map from an in-flight id to the cancel handle of that
// request's own context — and a second implementation would agree with the first
// on the day it was written and diverge silently afterwards, because nothing
// fails while two copies agree. What each frontend keeps for itself is the wire
// shape its own protocol spells a cancellation in; the id those shapes carry
// resolves here, through one canonicalization.
package requestcancel

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
)

// Registry maps an in-flight request's id to the cancel func of its per-request
// context, so a specific running request can be cancelled by id. It is the
// addressable face of the per-request cancel handle the dispatching code already
// holds: the registry stores that same func — it does not mint a second
// cancellation authority. The cancel authorities remain (1) ending the run,
// which cancels the parent context and so every derived per-request context at
// once, and (2) per-request cancel on completion, the child handle the
// dispatching code releases when its handler returns. The registry is a second
// address for that child handle, not a third authority.
//
// The per-request context a caller registers here is expected to be a plain
// cancellable chain derived from the run's own, so a deadline composes onto it by
// construction (context.WithDeadline / WithTimeout layers on with no structural
// change).
//
// The Registry tracks every id-bearing request for its in-flight lifetime and
// exposes a per-id cancel pickup. A frontend wires that pickup to whatever its
// protocol calls a cancellation notification: the dispatch loop routes that
// notification's id to Cancel, so a client cancels a specific in-flight request
// by id at runtime.
type Registry struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// New returns an empty Registry ready for concurrent Track / Untrack from
// several request goroutines.
func New() *Registry {
	return &Registry{cancels: map[string]context.CancelFunc{}}
}

// key canonicalizes a raw-JSON id into the registry's map key. The id reaches
// the registry from two wire surfaces — the request's own id (Track / Untrack)
// and the cancellation notification's id field (Cancel) — which a client may
// JSON-encode with different insignificant whitespace for the same logical id
// (`7` vs ` 7`, `"a"` vs `"a" `). json.Compact removes that whitespace so both
// surfaces resolve to one key and a cancel never silently misses an in-flight
// request over a formatting difference. A compaction error (malformed JSON,
// which a parsed id should never be) falls back to the raw bytes so the key is
// always defined.
//
// It stays unexported deliberately: a caller that could build a key of its own
// could reach the map by a route that skips this canonicalization, and the two
// routes would agree until the day a client sent whitespace. Keeping the
// primitive private means that bypass does not compile.
func key(id json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, id); err != nil {
		return string(id)
	}
	return buf.String()
}

// Track records cancel under the request's id for the duration of its handling.
// id is the raw-JSON id bytes the request carried, canonicalized so it resolves
// to the same key as the cancellation pickup. An empty id (a notification, which
// carries no id and cannot be cancelled) is not tracked. A duplicate live id is
// last-writer-wins — the later handler's cancel replaces the earlier under the
// same key.
func (r *Registry) Track(id json.RawMessage, cancel context.CancelFunc) {
	if len(id) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancels[key(id)] = cancel
}

// Untrack removes the registry entry for id. Called when a handler returns
// (paired with Track), so the registry holds only the currently in-flight
// requests. Removing an absent id is a no-op.
func (r *Registry) Untrack(id json.RawMessage) {
	if len(id) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cancels, key(id))
}

// Cancel looks up id's registered cancel func and calls it, cancelling that
// request's per-request context so its handler observes ctx.Err() at its next
// interruption point and returns. Cancelling an id that is not currently
// tracked — one that was never in flight, or one whose request has already been
// answered — is a safe no-op, which is what a late or invalid cancellation
// should amount to.
//
// This is the pickup point a frontend's cancellation-notification case calls,
// passing the id that notification carried.
func (r *Registry) Cancel(id json.RawMessage) {
	if len(id) == 0 {
		return
	}
	r.mu.Lock()
	c, ok := r.cancels[key(id)]
	r.mu.Unlock()
	if ok {
		c()
	}
}

// Len reports the number of currently-tracked in-flight requests. Tests assert
// it returns to zero after a burst drains, proving Track / Untrack stay balanced
// under concurrency.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cancels)
}
