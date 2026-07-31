// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof that a `uses` edge authored INSIDE a
// flattened sub-aggregate enters the topology: its target is namespaced to the
// flattened node ID, its developer per-edge `values` reach the consumer's
// render context, and a Release configures per-edge `infra` on it across the
// flattening boundary.

package compiler_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const subaggCatalog = "testdata/uses-edge-config/catalog/base"
const subaggLocalCatalog = "testdata/uses-edge-subagg/catalog/base"

// TestUsesEdgeSubagg_InternalEdgeEntersTopology pins the core fix: the
// sub-aggregate consumer's `databases.main → store` edge is present in the IR
// with its target namespaced to `data.store`, not dropped.
func TestUsesEdgeSubagg_InternalEdgeEntersTopology(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/uses-edge-subagg/release.yaml",
		CatalogPaths: []string{subaggCatalog, subaggLocalCatalog},
	})

	var found bool
	for _, e := range result.IRGraph.Spec.Edges {
		if e.Source == "data.consumer" && e.Socket == "databases" && e.Alias == "main" {
			found = true
			if e.Target != "data.store" {
				t.Errorf("sub-aggregate edge target = %q, want %q (childPrefix-namespaced)", e.Target, "data.store")
			}
		}
	}
	if !found {
		t.Fatalf("expected sub-aggregate-internal edge data.consumer/databases/main; got edges %v", result.IRGraph.Spec.Edges)
	}

	// A feature attached to the sub-aggregate consumer also carries a uses edge
	// to the sibling store; its target must be namespaced to data.store too
	// (else the origin-agnostic dangling-target sweep would false-halt this build).
	var featFound bool
	for _, e := range result.IRGraph.Spec.Edges {
		if e.Source == "data.consumer" && e.Socket == "peer" && e.OriginFeature == "core/sidecar" {
			featFound = true
			if e.Target != "data.store" {
				t.Errorf("feature edge target = %q, want %q (childPrefix-namespaced)", e.Target, "data.store")
			}
		}
	}
	if !featFound {
		t.Errorf("expected feature-attachment edge data.consumer/peer (origin core/sidecar) to enter the topology")
	}
}

// TestUsesEdgeSubagg_PerEdgeConfigReachesConsumer proves the developer per-edge
// `values` (authored inside the sub-aggregate) and the Release per-edge `infra`
// (configured across the flattening boundary) both reach the sub-aggregate
// consumer's rendered output — the per-edge config flows for sub-aggregate
// components exactly as for root components.
func TestUsesEdgeSubagg_PerEdgeConfigReachesConsumer(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/uses-edge-subagg/release.yaml",
		CatalogPaths: []string{subaggCatalog, subaggLocalCatalog},
	})
	merged := string(result.Artifacts["dist/cluster/compose/docker-compose.yaml"])

	for _, want := range []string{
		"grpc",   // developer values authored inside the sub-aggregate
		"50",     // Release per-edge infra pool_size, merged onto the sub-aggregate edge
		"mutual", // Release per-edge infra tls_mode
	} {
		if !strings.Contains(merged, want) {
			t.Errorf("sub-aggregate consumer output missing %q\n--- merged ---\n%s", want, merged)
		}
	}
}

// TestUsesEdgeSubagg_NonConformingInfraHalts pins the post-flatten guard
// coverage: a non-conforming per-edge infra (pool_size 999 over the socket
// max of 100) on a sub-aggregate component's edge now halts with
// ErrSchemaConformance. It built clean before sub-aggregate guard coverage —
// this is the probe from the verification run, now caught.
func TestUsesEdgeSubagg_NonConformingInfraHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/uses-edge-subagg/release-bad-infra.yaml",
		CatalogPaths: []string{subaggCatalog, subaggLocalCatalog},
	})
	if !errors.Is(err, resolver.ErrSchemaConformance) {
		t.Fatalf("want ErrSchemaConformance for an out-of-range per-edge infra on a sub-aggregate edge; got %v", err)
	}
}

// TestUsesEdgeSubagg_SocketDeclarationMarked pins the declaration marker on a
// FLATTENED SUB-AGGREGATE component, which is where the socket guards previously
// had a coverage gap and therefore where a marker hole would most plausibly open.
// One component authors two edges: one on a socket its type declares, one on a
// socket nothing declares. Both build, both reach the IR, and they carry the two
// contrasting marker values in a single graph.
func TestUsesEdgeSubagg_SocketDeclarationMarked(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/uses-edge-subagg/release-badsocket.yaml",
		CatalogPaths: []string{subaggCatalog, subaggLocalCatalog},
	})
	want := map[string]string{
		"databases":   types.SocketDeclarationDeclared,
		"nonexistent": types.SocketDeclarationAdHoc,
	}
	seen := map[string]string{}
	for _, e := range result.IRGraph.Spec.Edges {
		if e.Source != "mod.bad" {
			continue
		}
		seen[e.Socket] = e.SocketDeclaration
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("socket declaration markers on mod.bad = %v, want %v (edges: %+v)",
			seen, want, result.IRGraph.Spec.Edges)
	}
}

// TestUsesEdgeSubagg_AdHocSocketDanglingTargetStillHalts pins that an author-named
// socket is not a way to smuggle a non-existent target past the dangling sweep:
// the sweep is origin-agnostic and does not consult the declaration marker.
func TestUsesEdgeSubagg_AdHocSocketDanglingTargetStillHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/uses-edge-subagg/release-adhoc-dangle.yaml",
		CatalogPaths: []string{subaggCatalog, subaggLocalCatalog},
	})
	if !errors.Is(err, topology.ErrDanglingUsesTarget) {
		t.Fatalf("want ErrDanglingUsesTarget for an ad-hoc socket pointing at a non-existent target; got %v", err)
	}
}

// TestUsesEdgeSubagg_SubsystemWideFeatureProjects pins that a feature attached
// subsystem-wide INSIDE a sub-aggregate projects its uses edge into the IR,
// with the target namespaced to the flattened node ID — it was silently dropped
// before the cascade was captured at flatten time against the sub-aggregate's
// own subsystem tree.
func TestUsesEdgeSubagg_SubsystemWideFeatureProjects(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/uses-edge-subagg/release-scopefeat.yaml",
		CatalogPaths: []string{subaggCatalog, subaggLocalCatalog},
	})
	var found bool
	for _, e := range result.IRGraph.Spec.Edges {
		if e.Source == "data.svc" && e.Socket == "peer" && e.OriginFeature == "core/sidecar" {
			found = true
			if e.Target != "data.store3" {
				t.Errorf("subsystem-wide feature edge target = %q, want %q (childPrefix-namespaced)", e.Target, "data.store3")
			}
			// The `peer` socket is declared on the FEATURE's usesSchema, not on the
			// consuming component's — so this also pins that the declaration marker
			// reads the originating Feature for a Feature-authored edge. Reading the
			// consuming Component instead would mark every such edge ad-hoc, which
			// is the silently-wrong marker rather than a visibly-missing one.
			if e.SocketDeclaration != types.SocketDeclarationDeclared {
				t.Errorf("Feature-authored edge on a Feature-declared socket: SocketDeclaration = %q, want %q",
					e.SocketDeclaration, types.SocketDeclarationDeclared)
			}
		}
	}
	if !found {
		t.Fatalf("expected subsystem-wide-feature edge data.svc/peer (origin core/sidecar); got edges %v", result.IRGraph.Spec.Edges)
	}
}

// TestUsesEdgeSubagg_ParamMaterializes pins sub-aggregate parameter resolution:
// a sub-aggregate component's templated value ({{ .parameters.proto }}) is
// interpolated from the parent-supplied SubAggregate.Parameters (overriding the
// sub-aggregate's own default), reaching the consumer's rendered output.
func TestUsesEdgeSubagg_ParamMaterializes(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/uses-edge-subagg/release-paramsub.yaml",
		CatalogPaths: []string{subaggCatalog, subaggLocalCatalog},
	})
	merged := string(result.Artifacts["dist/cluster/compose/docker-compose.yaml"])
	// The parent passed proto: http (overriding the sub-aggregate default grpc);
	// the templated protocol value must materialize to "http", reaching DB_PROTOCOL.
	if !strings.Contains(merged, "http") {
		t.Errorf("sub-aggregate param did not materialize (want protocol http in output)\n--- merged ---\n%s", merged)
	}
	if strings.Contains(merged, "{{") {
		t.Errorf("un-interpolated template leaked into output:\n%s", merged)
	}
}

// TestUsesEdgeSubagg_ParamContractViolationHalts pins that the sub-aggregate's
// own parameter contract is enforced against the parent-supplied fulfillment:
// proto "ftp" (outside the sub-aggregate's enum) halts with
// ErrParameterContractViolation.
func TestUsesEdgeSubagg_ParamContractViolationHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/uses-edge-subagg/release-paramsub-badparam.yaml",
		CatalogPaths: []string{subaggCatalog, subaggLocalCatalog},
	})
	if !errors.Is(err, resolver.ErrParameterContractViolation) {
		t.Fatalf("want ErrParameterContractViolation for an out-of-enum sub-aggregate param; got %v", err)
	}
}

// TestUsesEdgeSubagg_PredicatePassesUnderSubAggParams pins predicate-aware
// conformance for sub-aggregate components: a value legal under the
// sub-aggregate's resolved parameters passes. The gated component's mode value
// "fast" must equal the allowed_mode parameter, which the parent sets to "fast".
func TestUsesEdgeSubagg_PredicatePassesUnderSubAggParams(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/uses-edge-subagg/release-predsub-ok.yaml",
		CatalogPaths: []string{subaggCatalog, subaggLocalCatalog},
	})
	if err != nil {
		t.Fatalf("value legal under the sub-aggregate's params should pass; got %v", err)
	}
}

// TestUsesEdgeSubagg_PredicateHaltsUnderSubAggParams pins the other half: the
// same value, illegal under a different sub-aggregate parameter value, halts.
// The parent sets allowed_mode "slow"; the component's mode "fast" fails the
// x-aggrega-validate predicate — evaluated against the SUB-AGGREGATE's params
// (it was silently skipped before predicate-aware conformance).
func TestUsesEdgeSubagg_PredicateHaltsUnderSubAggParams(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/uses-edge-subagg/release-predsub-bad.yaml",
		CatalogPaths: []string{subaggCatalog, subaggLocalCatalog},
	})
	if !errors.Is(err, resolver.ErrSchemaConformance) {
		t.Fatalf("want ErrSchemaConformance: mode 'fast' violates the predicate under allowed_mode 'slow'; got %v", err)
	}
}

// TestUsesEdgeSubagg_DanglingTargetHalts pins the origin-agnostic dangling
// sweep at depth: a sub-aggregate component wiring an edge to a non-existent
// target halts with ErrDanglingUsesTarget (the target namespaces to data.ghost,
// which is not a node).
func TestUsesEdgeSubagg_DanglingTargetHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/uses-edge-subagg/release-dangle.yaml",
		CatalogPaths: []string{subaggCatalog, subaggLocalCatalog},
	})
	if !errors.Is(err, topology.ErrDanglingUsesTarget) {
		t.Fatalf("want ErrDanglingUsesTarget for a dangling sub-aggregate edge; got %v", err)
	}
}

// TestUsesEdgeSubagg_SelectorTripsOnWrongType pins the origin-agnostic selector
// gate at depth: a sub-aggregate edge through the databases socket (selector
// type=stateful-server) to a core/cache target (type=cache) trips the analyzer
// selector check. The gate wraps ErrSelectorViolation into the joined build
// error, so the violation class is errors.Is-recoverable at the pipeline
// boundary even when the failing edge originates inside a sub-aggregate.
func TestUsesEdgeSubagg_SelectorTripsOnWrongType(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/uses-edge-subagg/release-selector.yaml",
		CatalogPaths: []string{subaggCatalog, subaggLocalCatalog},
	})
	if err == nil {
		t.Fatal("want a selector violation for a wrong-typed sub-aggregate target; got nil")
	}
	if !errors.Is(err, analyzer.ErrSelectorViolation) {
		t.Fatalf("want errors.Is(err, ErrSelectorViolation); got %v", err)
	}
	if !strings.Contains(err.Error(), "selector") {
		t.Fatalf("want a selector-violation diagnostic; got %v", err)
	}
}

// TestUsesEdgeSubagg_Deterministic pins Invariant 8 across the flattening
// boundary: repeated builds of the sub-aggregate fixture produce byte-identical
// rendered output and edge ordering.
func TestUsesEdgeSubagg_Deterministic(t *testing.T) {
	t.Parallel()
	build := func() (string, []string) {
		r := pipelinetest.Build(t, pipelinetest.Input{
			ReleasePath:  "testdata/uses-edge-subagg/release.yaml",
			CatalogPaths: []string{subaggCatalog, subaggLocalCatalog},
		})
		edges := make([]string, 0, len(r.IRGraph.Spec.Edges))
		for _, e := range r.IRGraph.Spec.Edges {
			edges = append(edges, e.Source+"|"+e.Socket+"|"+e.Alias+"|"+e.Target)
		}
		return string(r.Artifacts["dist/cluster/compose/docker-compose.yaml"]), edges
	}
	firstOut, firstEdges := build()
	for i := 0; i < 3; i++ {
		out, edges := build()
		if out != firstOut {
			t.Errorf("run %d: rendered output drifted", i)
		}
		if len(edges) != len(firstEdges) {
			t.Fatalf("run %d: edge count drift %d vs %d", i, len(edges), len(firstEdges))
		}
		for j := range edges {
			if edges[j] != firstEdges[j] {
				t.Errorf("run %d: edge order drift at %d: %q vs %q", i, j, edges[j], firstEdges[j])
			}
		}
	}
}
