// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package yamlpos

import "testing"

// A bracketed key spelling is not addressable, and the position it settles for is
// the same one a path naming nothing at all settles for.
//
// The walk splits on "." and matches each segment as a literal mapping key, or as
// a key followed by bracketed decimal element indices, which `["web"]` is not. So
// `components["web"]` is one segment that no key in any document spells, and the
// descent stops at the enclosing key it last matched — which is exactly
// what a path whose next segment is simply absent does. That collapse is the
// point of this test rather than an incidental observation: two findings about
// entirely different things, one naming a real key in a spelling this walk cannot
// read and one naming a key that is not there, arrive at one indistinguishable
// position.
//
// The unresolvable row is what makes the claim decisive. Without it, a reader
// could take the bracketed row's coarse answer for a deliberate coarsening —
// "close enough, it points at the right document region". With it, the answer is
// measured against the floor: the bracketed spelling buys nothing over naming a
// key that does not exist.
//
// The consequence a caller feels: every finding whose field path carries a
// bracketed key positions at the head of the document rather than on the offending
// entry, so an editor squiggles `spec:` for a fault several levels below it.
func TestFieldNodeReadsABracketedKeyNoDeeperThanAnAbsentOne(t *testing.T) {
	t.Parallel()

	doc := parseFieldDoc(t,
		`apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
spec:
  components:
    web:
      features:
        audit:
          type: hub.aggrega.ai/feature/core/audit
`)

	for _, tc := range []struct {
		name string
		path string
		// wantLine and wantColumn are the 1-based coordinates of the key the walk
		// settles on, written out rather than compared against a second walk, so
		// the row says something the implementation agreeing with itself cannot
		// satisfy.
		wantLine   int
		wantColumn int
		// wantKey is the key text at those coordinates, asserted alongside the
		// position because a position alone cannot say WHICH key was reached and a
		// coincidental line match would read as a pass.
		wantKey string
	}{
		{
			name:       "dotted segments reach the named key",
			path:       ".spec.components.web.features.audit",
			wantLine:   7,
			wantColumn: 9,
			wantKey:    "audit",
		},
		{
			name:       "a bracketed key stops at the last plain segment",
			path:       `.spec.components["web"].features["audit"]`,
			wantLine:   3,
			wantColumn: 1,
			wantKey:    "spec",
		},
		{
			// The floor the row above is measured against.
			name:       "a key that is not in the document stops at the same place",
			path:       ".spec.nonexistent.key",
			wantLine:   3,
			wantColumn: 1,
			wantKey:    "spec",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			node := FieldNode(doc, tc.path)
			if node == nil {
				t.Fatalf("FieldNode(%q) resolved to nothing; want the %q key at %d:%d",
					tc.path, tc.wantKey, tc.wantLine, tc.wantColumn)
			}
			if node.Value != tc.wantKey {
				t.Errorf("FieldNode(%q) reached key %q; want %q",
					tc.path, node.Value, tc.wantKey)
			}
			if node.Line != tc.wantLine || node.Column != tc.wantColumn {
				t.Errorf("FieldNode(%q) = %d:%d; want %d:%d",
					tc.path, node.Line, node.Column, tc.wantLine, tc.wantColumn)
			}
		})
	}

	// Stated as its own assertion because it is the claim a caller acts on, and
	// leaving it implicit in three rows would let a future change satisfy every
	// row while collapsing the two spellings onto one answer.
	dotted := FieldNode(doc, ".spec.components.web.features.audit")
	bracketed := FieldNode(doc, `.spec.components["web"].features["audit"]`)
	absent := FieldNode(doc, ".spec.nonexistent.key")
	if dotted == nil || bracketed == nil || absent == nil {
		t.Fatal("all three spellings resolve to some key, so the comparisons below have operands")
	}
	if dotted.Line == bracketed.Line && dotted.Column == bracketed.Column {
		t.Errorf("the two spellings of one path settled on one position (%d:%d), so this document "+
			"cannot tell them apart and the rows above prove nothing",
			dotted.Line, dotted.Column)
	}
	if bracketed.Line != absent.Line || bracketed.Column != absent.Column {
		t.Errorf("a bracketed key resolved to %d:%d and an absent key to %d:%d; the two are "+
			"expected to coincide, and a change that separates them changes what a bracketed "+
			"field path means",
			bracketed.Line, bracketed.Column, absent.Line, absent.Column)
	}
}
