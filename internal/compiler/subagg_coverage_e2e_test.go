// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — coverage parity for components that exist only after
// sub-aggregate flattening.
//
// Five rules were asked of root components and not of these, and every one failed
// the same way: a check whose subject was not in view skipped in silence, which is
// indistinguishable from a check that looked and approved. Measured before the
// closures, all five of these fixtures BUILT:
//
//   - a required uses socket left unbound produced a graph with zero edges;
//   - an inject block on a Black-Box slot rendered without complaint;
//   - a Release wiring a socket no usesSchema declares MATERIALISED the edge
//     (`data.consumer/nonexistent-socket/link -> data.store`);
//   - a Release per-edge `infra` of 9999 against a maximum of 50 was dropped
//     without being validated, because it reached no edge for the per-edge walk
//     to find;
//   - a component type declaring a required sizing key with no default was
//     satisfied by a deployment that wrote no sizing at all, and by one that
//     wrote a key the closed contract does not declare.

package compiler_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

func subaggCoverageInput(release string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath: "testdata/subagg-coverage/" + release,
		CatalogPaths: []string{
			"testdata/uses-edge-config/catalog/base",
			"testdata/subagg-coverage/catalog/base",
		},
	}
}

// TestSubaggCoverage_ControlBuilds is the counter-case the other four are read
// against: the same module, the same catalog, nothing wrong. Without it, a
// closure that rejected every sub-aggregate would look like four successes.
func TestSubaggCoverage_ControlBuilds(t *testing.T) {
	t.Parallel()
	result, err := pipelinetest.BuildE(t, subaggCoverageInput("release-ok.yaml"))
	if err != nil {
		t.Fatalf("the well-formed control must build; got %v", err)
	}
	if len(result.IRGraph.Spec.Edges) != 1 {
		t.Errorf("control must keep its one edge; got %v", result.IRGraph.Spec.Edges)
	}
}

// TestSubaggCoverage_UnboundRequiredSocketHalts — a socket the component type
// declares `required: true` that the module leaves unbound and no Release wires.
func TestSubaggCoverage_UnboundRequiredSocketHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, subaggCoverageInput("release-unbound-required.yaml"))
	if !errors.Is(err, resolver.ErrMissingRequiredSocket()) {
		t.Fatalf("want ErrMissingRequiredSocket for an unbound required socket on a sub-aggregate component; got %v", err)
	}
}

// TestSubaggCoverage_AuthoredSlotShapeHalts — the module's component authors an
// inject block on a Black-Box slot, whose empty allowlist reserves delivery to
// the Recipe.
func TestSubaggCoverage_AuthoredSlotShapeHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, subaggCoverageInput("release-bad-slot-shape.yaml"))
	if !errors.Is(err, resolver.ErrAttachmentSlotShape()) {
		t.Fatalf("want ErrAttachmentSlotShape for an authored delivery on a Black-Box slot inside a sub-aggregate; got %v", err)
	}
}

// TestSubaggCoverage_ReleaseUndeclaredSocketHalts — a Release per-edge entry
// naming a socket the consuming sub-aggregate component's usesSchema does not
// declare. It previously entered the IR as a real edge.
func TestSubaggCoverage_ReleaseUndeclaredSocketHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, subaggCoverageInput("release-relsocket.yaml"))
	if !errors.Is(err, resolver.ErrUnknownUsesSocket()) {
		t.Fatalf("want ErrUnknownUsesSocket for a Release edge into an undeclared socket on a sub-aggregate node; got %v", err)
	}
}

// TestSubaggCoverage_ReleaseDroppedEntryInfraHalts — a Release per-edge entry on
// a declared-but-unbound socket, carrying infra and no target. It reaches no
// edge, so the per-edge walk over resolved edges structurally cannot see it; the
// entry itself has to be read.
//
// The fault reported is that the configuration lands nowhere, which is the
// larger of the two things wrong with the entry: whether the value conforms to
// the socket's contract does not matter while nothing carries it. Conformance on
// a sub-aggregate edge that a Release entry DOES reach is pinned separately, by
// TestUsesEdgeSubagg_NonConformingInfraHalts.
func TestSubaggCoverage_ReleaseDroppedEntryInfraHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, subaggCoverageInput("release-relinfra.yaml"))
	if !errors.Is(err, topology.ErrUnboundReleaseEdgeConfig()) {
		t.Fatalf("want ErrUnboundReleaseEdgeConfig for per-edge infra on an entry that reached no edge; got %v", err)
	}
}

// TestSubaggCoverage_ComponentInfraConformance covers the component-level `infra`
// pillar on a module-contributed component: the operator-side half of a component
// type's contract, which the resolution-stage rule cannot reach because its
// subject is the root application's own instance list.
//
// The rows are the cells of the grid the two conditions make, not the margins of
// it. What binds the rule is a pair — the deployment placed the component, AND
// the sizing it placed does not satisfy the contract — so a row per condition
// would leave the interaction unstated. Placement itself has two spellings, and
// both are rows because the rule reads placement rather than a spelling: naming
// the component under a boundary's `components`, and routing the module that
// contributes it under `aggregates`. A routed entry says nothing about any one
// component, so its sizing is absent by construction, which is the sharpest form
// of the case a required key exists to catch.
//
// The unplaced row is the discriminating one and asserts BOTH directions. A
// component this deployment placed nowhere has a different fault with a different
// remedy, so the placement rule must be the one that speaks — and asserting only
// that it did would still pass if the sizing rule spoke alongside it, which is
// exactly the displacement the row exists to refuse.
func TestSubaggCoverage_ComponentInfraConformance(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		release string
		want    error
		// forbid is a sentinel the halt must NOT carry. Empty where the row has
		// nothing to exclude.
		forbid error
	}{
		{
			name:    "an entry that writes no sizing violates the required key",
			release: "release-sized-absent.yaml",
			want:    resolver.ErrSchemaConformance(),
		},
		{
			name:    "a routed module places every component and sizes none of them",
			release: "release-sized-routed.yaml",
			want:    resolver.ErrSchemaConformance(),
		},
		{
			name:    "a component this deployment placed nowhere is reported as unplaced, not as unsized",
			release: "release-sized-unplaced.yaml",
			want:    topology.ErrUnplacedComponent(),
			forbid:  resolver.ErrSchemaConformance(),
		},
		{
			name:    "an entry that writes the required key builds",
			release: "release-sized-present.yaml",
			want:    nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := pipelinetest.BuildE(t, subaggCoverageInput(tc.release))
			if tc.want == nil {
				if err != nil {
					t.Fatalf("the conforming deployment must build; got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v; got %v", tc.want, err)
			}
			if tc.forbid != nil && errors.Is(err, tc.forbid) {
				t.Fatalf("the halt must not also carry %v; got %v", tc.forbid, err)
			}
		})
	}
}
