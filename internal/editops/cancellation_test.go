// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package editops

import (
	"context"
	"errors"
	"testing"
)

// TestAWithdrawnEditWritesNothing proves the stop reaches the work rather than
// only the reply.
//
// The observable is the document's own bytes, not the returned error: a withheld
// reply looks identical whether the edit was applied or abandoned, so only
// reading the file back separates the two. A withdrawn edit must leave the
// document byte-for-byte as it was — the same outcome a schema refusal produces,
// which is what makes a withdrawal indistinguishable from an edit never
// attempted.
//
// No timing bound: the context is cancelled before the call, so where the stop
// lands is fixed by the interruption point rather than by machine speed.
func TestAWithdrawnEditWritesNothing(t *testing.T) {
	opts := Options{
		File:  "",
		Kind:  KindAddComponent,
		Node:  []string{"spec", "components"},
		Name:  "cache",
		Value: "core/redis:6.2.0",
	}

	t.Run("a withdrawn edit leaves the document byte-identical", func(t *testing.T) {
		root, rel := writeWorkspace(t, authoredAggregate)
		before := readBack(t, root, rel)

		o := opts
		o.File = rel

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		res, err := Apply(ctx, root, o)
		// t.Error rather than t.Fatal: the byte comparison below is the load-bearing
		// half of this proof, and a Fatal here would skip it on exactly the failure
		// it exists to characterise.
		if err == nil {
			t.Error("a withdrawn edit returned no error, so nothing distinguishes it from one that landed")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the edit refused with %v; want an error carrying context.Canceled, so a caller can tell a withdrawal from a refusal", err)
		}
		if len(res.Written) != 0 {
			t.Errorf("a withdrawn edit reported writing %v", res.Written)
		}
		if after := readBack(t, root, rel); after != before {
			t.Errorf("the withdrawn edit wrote the document anyway.\n"+
				"The interruption point is not ahead of the write, so bytes reached disk after the caller gave up.\nbefore:\n%s\nafter:\n%s",
				before, after)
		}
	})

	// The control. Without it "the document is unchanged" would read as a pass
	// over an edit that never changes anything, and the negative above would prove
	// nothing at all.
	t.Run("control: an uncancelled edit does write it", func(t *testing.T) {
		root, rel := writeWorkspace(t, authoredAggregate)
		before := readBack(t, root, rel)

		o := opts
		o.File = rel

		res, err := Apply(t.Context(), root, o)
		if err != nil {
			t.Fatalf("the control edit refused, so it establishes no baseline: %v", err)
		}
		if len(res.Written) == 0 {
			t.Fatal("the control edit reported writing nothing, so this request changes no document and the negative above is vacuous")
		}
		if after := readBack(t, root, rel); after == before {
			t.Fatal("the control edit left the document byte-identical, so its being unchanged above is not evidence of a stop")
		}
	})
}
