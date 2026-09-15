// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import "encoding/json"

// cancelParams is the params shape for the $/cancelRequest notification. ID is
// captured as raw JSON — not decoded to a concrete int/string — so it
// round-trips to the registry's key through the same canonicalization the
// in-flight request's id took, with no decode-then-re-encode step that could
// perturb the bytes.
//
// It is deliberately NOT shared with the other request-serving frontend, whose
// own cancellation notification spells the same field differently. The two are
// two protocols' wire spellings of one concept, not one mechanic written twice:
// a single struct answering to both wires would have to accept each protocol's
// field name on the other's surface, which is a wider contract than either
// protocol states. The mechanic itself — the registry the id resolves against,
// and the canonicalization that resolves it — has exactly one home, in
// internal/requestcancel, which both frontends import. One registry; one key
// rule; two wire shapes routing into it.
type cancelParams struct {
	ID json.RawMessage `json:"id"`
}

// What covers the daemon's registry call sites, now that the registry's own unit
// tests live with the registry in internal/requestcancel.
//
// The moved tests followed the primitive. Nothing followed the call sites they
// left behind, and a behaviour-preserving relocation leaves a caller uncovered
// while every moved test still passes — so each site is named here with the test
// that covers it from this package.
//
//   - dispatchLoop's lazy construction (requestcancel.New when cancelTracker is
//     nil): TestCancelRequestNotificationInterruptsHandler and
//     TestCancelTrackerEmptyAfterBurst both enter dispatchLoop without presetting
//     the field, so a construction that did not happen would nil-panic there.
//
//   - the $/cancelRequest acceptance-boundary case (Cancel):
//     TestCancelRequestNotificationInterruptsHandler drives the real notification
//     and asserts the held handler observes cancellation;
//     TestCancelRequestUnknownIdIsNoOp drives one for an id nothing tracks and
//     asserts the loop serves on; TestCancelRequestNormalizesIdWhitespace drives
//     one whose id spelling differs from the request's and asserts it still
//     lands, which is this package's end-to-end cover for the key
//     canonicalization the registry keeps private.
//
//   - handle's Track: TestCancelRequestByIdInterruptsHandler reaches the registry
//     by id against a live in-flight handler, which only resolves if handle
//     tracked it.
//
//   - handle's deferred Untrack: TestCancelTrackerEmptyAfterBurst asserts the
//     registry is empty after a 50-request burst drains, which is the Track /
//     Untrack balance under the pool.
//
//   - the Len read is test-only and is covered by the test that performs it.
