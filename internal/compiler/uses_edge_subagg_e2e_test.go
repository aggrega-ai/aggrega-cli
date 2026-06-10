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
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
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

// TestUsesEdgeSubagg_UndeclaredSocketHalts pins socket-allowlist coverage for
// sub-aggregate components: a component inside a sub-aggregate authoring a
// socket its type's usesSchema does not declare halts with ErrUnknownUsesSocket
// (the target exists, so this is the socket guard, not the dangling sweep).
func TestUsesEdgeSubagg_UndeclaredSocketHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/uses-edge-subagg/release-badsocket.yaml",
		CatalogPaths: []string{subaggCatalog, subaggLocalCatalog},
	})
	if !errors.Is(err, resolver.ErrUnknownUsesSocket) {
		t.Fatalf("want ErrUnknownUsesSocket for an undeclared socket on a sub-aggregate component; got %v", err)
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
		}
	}
	if !found {
		t.Fatalf("expected subsystem-wide-feature edge data.svc/peer (origin core/sidecar); got edges %v", result.IRGraph.Spec.Edges)
	}
}
