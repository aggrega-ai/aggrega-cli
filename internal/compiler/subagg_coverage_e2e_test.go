// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — coverage parity for components that exist only after
// sub-aggregate flattening.
//
// Four rules were asked of root components and not of these, and every one failed
// the same way: a check whose subject was not in view skipped in silence, which is
// indistinguishable from a check that looked and approved. Measured before the
// closures, all four of these fixtures BUILT:
//
//   - a required uses socket left unbound produced a graph with zero edges;
//   - an inject block on a Black-Box slot rendered without complaint;
//   - a Release wiring a socket no usesSchema declares MATERIALISED the edge
//     (`data.consumer/nonexistent-socket/link -> data.store`);
//   - a Release per-edge `infra` of 9999 against a maximum of 50 was dropped
//     without being validated, because it reached no edge for the per-edge walk
//     to find.

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
