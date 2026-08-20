// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import "testing"

// TestDocumentsAndGraphAreUnreachableFromARule is the enforcement for the
// property the framework's declarations rest on: a rule reads named facts, so it
// cannot reach a whole authored document or the finalised graph and derive a fact
// for itself. Placement, caller coverage and traversal are all computed from what
// a rule declared it reads, so they are sound only while that declaration is
// exhaustive — and it is exhaustive only while there is nothing whole to derive
// from.
//
// It is a compile gate rather than a scan of the source, and the two are not
// interchangeable. A scan reads the text of the accessors as they are spelled
// today, so it reports a clean tree for a document reached through a local
// renaming, and it reports a clean tree again the moment someone adds a route it
// was not written to look for. A compile failure is answered in the type
// identity the language works in, where a renaming is not available and an
// accessor that exists is found whatever it is called at the point of use.
//
// # What it covers
//
// Each program under testdata takes one route a rule would actually take, and
// must fail to build:
//
//   - ask the view for the document;
//   - widen the view it was handed back down to the document;
//   - the same, with every document renamed first, which is the route a scan
//     cannot see;
//   - ask the subject instead of the view.
//
// The routes are probed against the widest view in the containment chain, and
// that is what makes probing one view enough. The chain is one line by
// construction — the generator refuses a stage that two others extend, and
// refuses a second root, because an unordered pair would leave the earliest
// stage a fact is complete at un-computable — so a contained view exposes a
// subset of the widest one's methods, and a document unreachable from the widest
// is unreachable from every one of them. The containment is asserted, not
// described: the control program assigns the probed view to each of the others,
// which compiles only while it holds.
//
// # What it does not cover, stated because an unstated bound reads as full cover
//
// Three things, all real.
//
// A chain EXTENDED past the probed view is not caught. The control's containment
// assertions still hold when a stage is appended, so the new stage's own
// accessors would go unprobed; adding a stage owes a line in the control. A
// BRANCHED chain is caught, both by the generator and by the control.
//
// A rule may still name a document as its own view type: the contract a view
// satisfies declares no method, so it admits any type, and the framework's
// traversal will accept and invoke such a rule. What that costs is what the
// framework's own accounting is for — such a rule declares as read something no
// stage produces, which the declaration-narrowing read reports — but it is not
// refused by the compiler and this gate does not claim it is.
//
// And the subject route is probed against the contract every subject kind
// embeds, so what it establishes is that the shared contract offers no route. A
// kind declaring an accessor of its own would not be refused here.
//
// The scan over the generated accessors is the complement rather than the
// predecessor of this gate, and both are kept: the scan enumerates every
// accessor and cannot see a renaming, this gate sees renamings and enumerates
// nothing.
func TestDocumentsAndGraphAreUnreachableFromARule(t *testing.T) {
	t.Parallel()

	cases := []struct {
		dir string
		// route names what the program attempts, for the failure message.
		route string
		// wantAny is satisfied when the compiler output contains every
		// substring in one of its groups. Groups exist so a message whose
		// exact wording differs across toolchain versions can still be
		// matched on the part that carries the meaning.
		wantAny [][]string
	}{
		{
			dir:   "reach_view_accessor",
			route: "asking the view for a whole document",
			wantAny: [][]string{
				{
					"has no field or method Aggregate",
					"has no field or method Release",
					"has no field or method Graph",
					"has no field or method IRGraph",
				},
				{"v.Aggregate undefined", "v.Release undefined", "v.Graph undefined"},
			},
		},
		{
			dir:   "reach_view_assertion",
			route: "widening the view back down to a whole document",
			wantAny: [][]string{
				{
					"impossible type assertion",
					"*types.Aggregate",
					"*types.Release",
					"*types.IRGraph",
				},
			},
		},
		{
			dir:   "reach_view_alias",
			route: "widening the view back down to a document renamed first",
			wantAny: [][]string{
				{
					"impossible type assertion",
					"deployedApplication",
					"environmentBinding",
					"wiring",
				},
			},
		},
		{
			dir:   "reach_subject",
			route: "asking the subject for a whole document",
			wantAny: [][]string{
				{
					"has no field or method Aggregate",
					"has no field or method Release",
					"has no field or method Graph",
				},
				{"s.Aggregate undefined", "s.Release undefined", "s.Graph undefined"},
			},
		},
	}

	// Calibration first: if the control does not compile, every verdict below is
	// meaningless, so this is a hard stop rather than one failure among many. The
	// control is also where the containment chain the coverage claim rests on is
	// asserted, so a chain that stopped being one line stops this test here
	// rather than leaving it quietly probing a view that is no longer the
	// deepest.
	if out, err := compileTestdata(t, "reach_control"); err != nil {
		t.Fatalf("the calibration program under testdata/reach_control must compile, "+
			"otherwise the cases below would all fail for an unrelated reason — and it is "+
			"also where the view containment chain is asserted, so read the error before "+
			"assuming an import moved.\ngo build error: %v\noutput:\n%s", err, out)
	}

	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			t.Parallel()

			out, err := compileTestdata(t, tc.dir)
			if err == nil {
				t.Fatalf("testdata/%s compiled, so %s is possible. A rule reads named facts and "+
					"nothing whole; if that is no longer true, a rule can derive a fact it never "+
					"declared, and the placement, caller coverage and traversal computed from its "+
					"declaration no longer describe what it reads.", tc.dir, tc.route)
			}
			if !containsAnyGroup(out, tc.wantAny) {
				t.Errorf("testdata/%s failed to compile, but for an unrecognised reason — "+
					"the failure must be the reach being refused, not something else.\n"+
					"want output matching one of %v\ngot:\n%s", tc.dir, tc.wantAny, out)
			}
		})
	}
}
