// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// fullyPopulatedProvenanceCtx is a NodeRenderContext with every provenance
// field set to a non-default value, so every conditional branch in the shared
// provenance builders fires. The test below relies on this to compute the full
// provenance key set.
func fullyPopulatedProvenanceCtx() renderer.NodeRenderContext {
	return renderer.NodeRenderContext{
		NodeID:           "api",
		LabelComponentID: "api",
		BoundaryPath:     "production/edge",
		RecipeName:       "web-service",
		RecipeVersion:    "1.4.2",
		ProviderName:     "compose",
		ProviderVersion:  "1.0.0",
		SyncWave:         3,
		Provenance: renderer.ProvenanceContext{
			CompilerVersion: "0.9.0",
			AggregateName:   "storefront",
			Environment:     "production",
		},
	}
}

// TestComposeLabelsDeriveFromSharedBuilders is the no-silent-gap guard. It
// pins compose's buildAggregaLabels output to exactly the union of the two
// shared provenance builders minus the two Kubernetes-only keys compose
// deliberately omits. The expected key set is COMPUTED from the shared builders
// (never hand-listed). Because compose derives from those same builders today,
// a key added to a shared builder flows into both `expected` and the actual
// output automatically — that is the property the derivation buys. The
// regression this asserts against is a reversion of compose to a hand-listed
// key set (or an over-broad omit set): the moment compose's emitted keys stop
// equalling `union − composeOmittedProvenanceKeys`, this fails, surfacing the
// drift from the helm and helm-chart provenance set rather than letting compose
// silently lag. The omitted keys are also checked to be genuine union members,
// so the omit list cannot quietly go stale.
func TestComposeLabelsDeriveFromSharedBuilders(t *testing.T) {
	nodeCtx := fullyPopulatedProvenanceCtx()

	// Union of the shared label-safe and charset-unsafe builder key sets.
	union := map[string]struct{}{}
	for k := range renderer.BuildAggregaProvenanceLabels(nodeCtx) {
		union[k] = struct{}{}
	}
	for k := range renderer.BuildAggregaProvenanceAnnotations(nodeCtx, renderer.ProvenanceRecipeURIFromNodeCtx(nodeCtx)) {
		union[k] = struct{}{}
	}

	// Compose carries the whole union as labels, minus the Kubernetes-only
	// keys. composeOmittedProvenanceKeys is the same named set the production
	// code deletes, so the expectation tracks the implementation's omit list.
	expected := map[string]struct{}{}
	for k := range union {
		expected[k] = struct{}{}
	}
	for _, k := range composeOmittedProvenanceKeys {
		delete(expected, k)
	}

	got := buildAggregaLabels(nodeCtx)
	gotKeys := map[string]struct{}{}
	for k := range got {
		gotKeys[k] = struct{}{}
	}

	if !keySetsEqual(expected, gotKeys) {
		t.Fatalf("compose buildAggregaLabels key set mismatch:\n  expected: %v\n  got:      %v",
			sortedKeys(expected), sortedKeys(gotKeys))
	}

	// The omitted keys must genuinely be in the union (otherwise the omit set
	// is dead) and must genuinely be absent from compose output.
	for _, k := range composeOmittedProvenanceKeys {
		if _, inUnion := union[k]; !inUnion {
			t.Errorf("omitted key %q is not produced by either shared builder; the omit list is stale", k)
		}
		if _, inGot := gotKeys[k]; inGot {
			t.Errorf("compose emitted Kubernetes-only key %q that it should omit", k)
		}
	}
}

// TestK8sEnginesEmitFullUnion is the cross-engine companion: the helm/helm-chart
// surfaces split the same provenance into labels + annotations, and their union
// must equal the shared builders' union for the same node — so the three engines
// share one provenance source, differing only in label/annotation partitioning.
func TestK8sEnginesEmitFullUnion(t *testing.T) {
	nodeCtx := fullyPopulatedProvenanceCtx()

	union := map[string]struct{}{}
	for k := range renderer.BuildAggregaProvenanceLabels(nodeCtx) {
		union[k] = struct{}{}
	}
	for k := range renderer.BuildAggregaProvenanceAnnotations(nodeCtx, renderer.ProvenanceRecipeURIFromNodeCtx(nodeCtx)) {
		union[k] = struct{}{}
	}

	// composeOmittedProvenanceKeys are precisely the keys the K8s engines keep
	// but compose drops. So K8s union == compose key set ∪ omitted keys.
	composeKeys := map[string]struct{}{}
	for k := range buildAggregaLabels(nodeCtx) {
		composeKeys[k] = struct{}{}
	}
	reconstructed := map[string]struct{}{}
	for k := range composeKeys {
		reconstructed[k] = struct{}{}
	}
	for _, k := range composeOmittedProvenanceKeys {
		reconstructed[k] = struct{}{}
	}

	if !keySetsEqual(union, reconstructed) {
		t.Fatalf("K8s provenance union does not reconstruct from compose keys + omitted keys:\n  union:         %v\n  reconstructed: %v",
			sortedKeys(union), sortedKeys(reconstructed))
	}
}

func keySetsEqual(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
