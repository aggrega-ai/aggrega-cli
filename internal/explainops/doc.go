// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package explainops composes the explanation of a halting condition from a code
// a reader is holding, for every surface that answers the question.
//
// # Why the composition lives here rather than at a surface
//
// The answer is assembled from two places and the assembly has an order, a
// taxonomy of four distinct failures, and a rule about which half is reported
// when the other is missing. A surface that built that for itself would agree
// with the next surface on the day it was written and part company afterwards,
// and the parting would be invisible: both would still answer, and a reader
// asking two surfaces about one code would be told two different things about the
// same condition. So the composition has one home and each surface is a transport
// over it — one renders the result as text for a terminal, another as a payload
// on a wire — and neither decides what the answer IS.
//
// # The two halves, and why they come from different places
//
// The condition itself — its two codes and the sentence every diagnostic naming
// it opens with — is declared inside the binary, so that much is always available
// and needs no network and no cache. The walk-through, which is what the
// condition means and what to change, is prose written for a reader, so it is
// served from the documentation bundle kept in the machine-wide cache, read under
// the workspace's own signature check.
//
// A code that resolves therefore always produces an answer, and the two ways the
// prose can be missing are reported as themselves: a condition the published page
// does not document yet, and a bundle this workspace cannot read — none on the
// machine, or a copy its check does not admit. Both leave the condition standing,
// which is why they travel as a note beside the answer rather than as a refusal of
// it.
//
// # The pages travel together, in one read
//
// [Pages] carries both published pages because they come from one bundle and one
// read. Fetching them separately would let a reply resolve a code against one
// version of the documentation and take its prose from another.
//
// [Compose] takes the read as a function rather than as a value so that a code it
// can answer without reading anything costs no read: a retired number is answered
// from the retirement record alone, and acquiring a documentation bundle to learn
// what the record already said would spend a round trip for nothing.
package explainops
