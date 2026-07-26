// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// lintOverriddenAnnotation walks every IRFeatureEntry's
// per-pillar provenance maps and emits one advisory warning for each
// leaf where an explicit contribution suppressed an annotation
// contribution. The lint surfaces the authoring scenario where a
// developer-authored explicit attachment ends up overriding an
// annotation-driven contribution at the same leaf — the author may
// be unaware of the suppression because the annotation's contribution
// no longer appears in the rendered output.
//
// The lint fires per leaf where FieldProvenance.OverriddenAnnotation
// is non-nil, regardless of whether the explicit value would have
// produced an output difference from the suppressed annotation
// value. The IR builder's per-leaf merge overwrites the suppressed
// leaf value with the explicit value at merge time, so the suppressed
// value is not recoverable from the IR; the per-provenance signal is
// the actionable diagnostic — operators reading the warning learn
// the specific authoring conflict, which they investigate from the
// authoring side.
//
// The walker iterates nodes in deterministic order (node-ID lexical
// sort) and emits one warning string per overriding leaf in the
// order it encounters them within each node's author-order Feature
// map and the per-pillar OrderedMap iteration. Same input produces
// byte-identical warnings across runs.
func lintOverriddenAnnotation(graph *types.IRGraph) []string {
	if graph == nil {
		return nil
	}
	nodeIDs := make([]string, 0, graph.Spec.Nodes.Len())
	for id := range graph.Spec.Nodes.All() {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)

	var out []string
	for _, nodeID := range nodeIDs {
		features, hasFeatures := graph.Spec.FeatureValuesMapping.Get(nodeID)
		if !hasFeatures {
			continue
		}
		for instanceName, entry := range features.All() {
			collectOverrideWarnings(nodeID, instanceName, "values", entry.ValuesProvenance, &out)
			collectOverrideWarnings(nodeID, instanceName, "infra", entry.InfraProvenance, &out)
		}
	}
	return out
}

// collectOverrideWarnings appends one warning per leaf where the
// per-leaf FieldProvenance carries a non-nil OverriddenAnnotation
// reference. The warning text names the node, the Feature instance,
// the pillar, the leaf, and the suppressed annotation's key + source
// so operators can navigate from the diagnostic to the authoring
// site without further plumbing.
func collectOverrideWarnings(nodeID, instanceName, pillar string, provenance types.OrderedMap[types.FieldProvenance], out *[]string) {
	for leaf, prov := range provenance.All() {
		if prov.OverriddenAnnotation == nil {
			continue
		}
		suppressed := prov.OverriddenAnnotation
		*out = append(*out, fmt.Sprintf(
			"LINT: node %q feature %q pillar %q leaf %q: explicit attachment overrides annotation %q from %s",
			nodeID, instanceName, pillar, leaf,
			suppressed.AnnotationKey, suppressed.AnnotationSource,
		))
	}
}
