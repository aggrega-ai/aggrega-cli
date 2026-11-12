// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// legalPlacementRelease places four things on all three axes and places nothing
// twice, so it is the document the invocation proofs below are read against: a
// rule reporting nothing on it is a rule that found nothing rather than a rule
// nothing asked.
const legalPlacementRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  boundaries:
    outer:
      directory: ./outer
      components:
        api: {}
      aggregates:
        data:
          components:
            store: {}
      boundaries:
        inner:
          directory: ./inner
          components:
            worker: {}
`

// TestBoundaryPlacementTraversalEnumeratesEveryAuthoredPlacement is the half a
// green refusal cannot supply: a rule invoked zero times and a rule that found
// nothing report the same thing, so the enumeration is counted separately.
//
// The production route is LoadReleaseFromBytes, which asks
// rejectDuplicateBoundaryComponents, which hands this same presentation to
// [constraint.RunOnBoundaryPlacements]. That route is driven first, and its clean
// load is what the count below is read against.
func TestBoundaryPlacementTraversalEnumeratesEveryAuthoredPlacement(t *testing.T) {
	t.Parallel()
	if _, err := LoadReleaseFromBytes([]byte(legalPlacementRelease), "release.yaml"); err != nil {
		t.Fatalf("a Release placing nothing twice must load clean; got %v", err)
	}

	presented := newReleaseDocumentView("release.yaml", parseReleaseForPlacements(t, legalPlacementRelease))
	enumerated := presented.BoundaryPlacements()
	if len(enumerated) == 0 {
		t.Fatal("the placement axis enumerated NOTHING on a legal Release: a rule over it " +
			"would be invoked zero times and would report clean for that reason alone")
	}

	// Counted through the traversal rather than off the enumerator, because what
	// has to be distinguishable is the number of times a rule is INVOKED.
	invoked := 0
	counting := constraint.Check[constraint.DocumentView, constraint.BoundaryPlacementSubject]{
		Sentinel: ErrDuplicateBoundaryComponent(),
		Severity: constraint.SeverityFatal,
		Run: func(
			_ constraint.DocumentView,
			_ constraint.BoundaryPlacementSubject,
		) []constraint.Objection {
			invoked++
			return nil
		},
	}
	objections, err := constraint.RunOnBoundaryPlacements(
		nil, presented, constraint.DocumentView(presented), counting)
	if err != nil {
		t.Fatalf("the traversal must not fail on a legal Release; got %v", err)
	}
	if len(objections) != 0 {
		t.Fatalf("a check raising nothing must produce no objections; got %d", len(objections))
	}
	if invoked != len(enumerated) {
		t.Fatalf("the traversal must invoke the check once per placement; enumerated %d, invoked %d",
			len(enumerated), invoked)
	}

	// The whole set, named rather than counted, so a placement silently dropped
	// from one axis fails here instead of leaving a smaller number that still
	// reads as non-zero.
	want := []constraint.BoundaryPlacement{
		{
			Document:     constraint.AuthoringDocumentRelease,
			BoundaryPath: "outer",
			Reference:    "api",
			Axis:         constraint.PlacementNamedComponent,
		},
		{
			Document:     constraint.AuthoringDocumentRelease,
			BoundaryPath: "outer",
			Reference:    "data.store",
			Axis:         constraint.PlacementRoutedComponent,
		},
		{
			Document:     constraint.AuthoringDocumentRelease,
			BoundaryPath: "outer",
			Reference:    "data",
			Axis:         constraint.PlacementRoutedModule,
		},
		{
			Document:     constraint.AuthoringDocumentRelease,
			BoundaryPath: "outer/inner",
			Reference:    "worker",
			Axis:         constraint.PlacementNamedComponent,
		},
	}
	reported := presented.ReleaseBoundaryPlacements()
	if len(reported) != len(want) {
		t.Fatalf("the fact must report one entry per placement; want %d, got %d (%v)",
			len(want), len(reported), reported)
	}
	for i := range want {
		if reported[i] != want[i] {
			t.Errorf("placement %d: want %+v, got %+v", i, want[i], reported[i])
		}
	}
}

// TestBoundaryPlacementLocatorNamesTheAuthoredKeyOnEveryAxis pins the locator each
// axis produces, because the locator is what the migration from a hand-assembled
// field path had to preserve.
//
// The boundary nesting is spelled level by level under the container's own key,
// which is how the document spells a nested boundary; a path joined in as one
// segment would name a key no file contains. An entry in the boundary
// normalization synthesized names the top-level shape the author did write,
// because that boundary is not a scope any file spells.
func TestBoundaryPlacementLocatorNamesTheAuthoredKeyOnEveryAxis(t *testing.T) {
	t.Parallel()
	const topLevelOnly = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  components:
    api: {}
`
	for _, tc := range []struct {
		name     string
		document string
		want     []string
	}{
		{
			name:     "every axis of a nested boundary tree",
			document: legalPlacementRelease,
			want: []string{
				".spec.boundaries.outer.components.api",
				".spec.boundaries.outer.aggregates.data.components.store",
				".spec.boundaries.outer.aggregates.data",
				".spec.boundaries.outer.boundaries.inner.components.worker",
			},
		},
		{
			name:     "an entry in the boundary normalization synthesized",
			document: topLevelOnly,
			want:     []string{".spec.components.api"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			presented := newReleaseDocumentView("release.yaml", parseReleaseForPlacements(t, tc.document))
			var got []string
			collecting := constraint.Check[constraint.DocumentView, constraint.BoundaryPlacementSubject]{
				Sentinel: ErrDuplicateBoundaryComponent(),
				Severity: constraint.SeverityFatal,
				Run: func(
					_ constraint.DocumentView,
					s constraint.BoundaryPlacementSubject,
				) []constraint.Objection {
					got = append(got, s.Locator())
					return nil
				},
			}
			if _, err := constraint.RunOnBoundaryPlacements(
				nil, presented, constraint.DocumentView(presented), collecting); err != nil {
				t.Fatalf("the traversal must not fail; got %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("want %d locator(s), got %d: %q", len(tc.want), len(got), got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("locator %d: want %q, got %q", i, tc.want[i], got[i])
				}
			}
		})
	}
}

// TestReleaseBoundaryPlacementsReportsDeclarationsNotTheReconciledSet is the
// fact's own contract, demonstrated rather than asserted.
//
// The fact declares that it reports what the document DECLARES and never what the
// reconciled component set holds, and that the two differ exactly on the shape the
// rule over it exists to refuse. So the demonstration is the contrast: for a
// boundary naming one component directly and again through a routing entry, the
// fact reports TWO placements at that id while the reconciled reading beside it
// folds them to ONE.
//
// The document is parsed rather than loaded, because loading it is what the rule
// refuses — which is the point: the fact has to see the collision the reconciled
// reading cannot, and there is no window in which it does not.
func TestReleaseBoundaryPlacementsReportsDeclarationsNotTheReconciledSet(t *testing.T) {
	t.Parallel()
	const namedAndRouted = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  boundaries:
    east:
      directory: ./east
      components:
        data.store:
          infra:
            replicas: 1
      aggregates:
        data:
          components:
            store:
              infra:
                replicas: 5
`
	rel := parseReleaseForPlacements(t, namedAndRouted)

	declared := 0
	for _, placement := range newReleaseDocumentView("release.yaml", rel).ReleaseBoundaryPlacements() {
		if placement.Reference == "data.store" {
			declared++
		}
	}
	if declared != 2 {
		t.Errorf("the fact must report both entries that resolve to one id; got %d", declared)
	}

	east, found := rel.Spec.Boundaries.Get("east")
	if !found {
		t.Fatal("want the east boundary")
	}
	held := types.BoundaryComponents(east)
	reconciled := 0
	for name := range held.All() {
		if name == "data.store" {
			reconciled++
		}
	}
	if reconciled != 1 {
		t.Fatalf("the reconciled reading is expected to fold the two entries to one; got %d — "+
			"if this ever reports two, the contrast the fact's contract rests on has gone", reconciled)
	}
}

// parseReleaseForPlacements unmarshals a Release without asking the placement
// rule, which the load path asks. It exists for the one proof that needs a
// document the rule refuses: reading such a document through the loader is
// impossible by design, and the fact's contract is exactly about what it sees in
// it. It parses the same bytes the loader parses, so nothing is hand-assembled.
func parseReleaseForPlacements(t *testing.T, document string) *types.Release {
	t.Helper()
	var rel types.Release
	if err := yaml.Unmarshal([]byte(document), &rel); err != nil {
		t.Fatalf("the fixture must unmarshal; got %v", err)
	}
	return &rel
}
