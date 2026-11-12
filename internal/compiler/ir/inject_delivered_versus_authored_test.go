// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package ir

import (
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestTheDeliveredKeysAreNotTheAuthoredKeys pins the divergence a collision rule
// over what a connection delivers depends on.
//
// One authored injection block has two readings. The authored one names every
// entry whose identifier decodes; the delivered one names only the entries the
// graph can carry as one string per key, and drops the rest. A rule refusing a
// key two connections both deliver has to read the delivered side: a key nothing
// carries reaches no consumer, sets nothing, and therefore collides with
// nothing, so a rule reading the authored side would refuse a pairing the build
// never produces — refusing correct work, which is worse than the silent
// displacement such a rule exists to stop.
//
// The two readings agree on every entry the document schema admits today, and
// that is exactly why this is asserted rather than left to be noticed: an
// agreement no test compares is an agreement nothing would report the loss of,
// and the readings would then be free to drift into one another. The block below
// is built in memory rather than parsed, because the shape it turns on is one
// the authoring schema currently refuses at the envelope — which makes the
// conversion's defensive drop the only place the distinction is observable.
func TestTheDeliveredKeysAreNotTheAuthoredKeys(t *testing.T) {
	t.Parallel()

	// Three entries under one mechanism. The first two are ordinary and are
	// carried. The third names an identifier but its payload is neither a
	// literal string nor a reference to a published key, so there is nothing to
	// deliver under it.
	block := &types.InjectBlock{Env: []any{
		map[string]any{"in": "CARRIED_LITERAL", "value": "a"},
		map[string]any{"in": "CARRIED_REFERENCE", "from": "host"},
		map[string]any{"in": "UNCARRIED", "value": 123},
	}}

	authored := injectDeliveryKeys(block, "env")
	wantAuthored := []string{"CARRIED_LITERAL", "CARRIED_REFERENCE", "UNCARRIED"}
	if !slices.Equal(authored, wantAuthored) {
		t.Errorf("the authored reading names %v, want every entry whose identifier decodes: %v",
			authored, wantAuthored)
	}

	converted := convertInjectBlock(block)
	delivered, carries := converted.Get("env")
	if !carries {
		t.Fatal("the delivered reading carries no entry at all for a mechanism with two ordinary entries")
	}
	got := delivered.Keys()
	wantDelivered := []string{"CARRIED_LITERAL", "CARRIED_REFERENCE"}
	if !slices.Equal(got, wantDelivered) {
		t.Errorf("the delivered reading names %v, want only the entries the graph carries: %v",
			got, wantDelivered)
	}

	// The point of the proof, stated as its own assertion so a future change
	// that made the two readings identical fails here rather than passing as
	// two assertions that happen to agree.
	if slices.Equal(authored, got) {
		t.Error("the two readings name the same keys; a rule over the delivered side would " +
			"now be indistinguishable from one over the authored side, and the distinction " +
			"is what keeps a delivery that reaches no consumer from being refused")
	}
}
