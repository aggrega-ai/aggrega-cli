// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// resolveOutputLabels builds the author-owned label set a node's rendered output
// carries alongside the fixed build-provenance labels: the recipe's literal
// spec.outputLabels, plus one entry for every key spec.outputComponentLabels
// names, whose value is read from that key on the node's own component labels.
// Promotion is by reference — the recipe names a key and the value follows the
// component that key sits on, so the stamped value cannot drift from the
// identity it came from.
//
// The returned map is always freshly allocated, which is a correctness
// requirement rather than a matter of style. A recipe handed to a render is a
// shallow copy of the catalog's entity, so its outputLabels field carries the
// same underlying map as the catalog entry and as every other node bound to that
// recipe. Writing a promoted value into that map would stamp one node's identity
// onto the recipe every later node reads: a node whose component omits the key
// would inherit a neighbour's value, and the emitted bytes would depend on how
// many nodes rendered first.
//
// A named key the node's component does not declare stamps nothing and raises a
// non-fatal advisory through warn, so the discrepancy is surfaced and the build
// proceeds. warn may be nil, which drops the advisory.
//
// Copying the literal map by range is not an ordering hazard: the destination is
// a map, so the order entries are written in is not observable, and every surface
// that emits labels sorts its keys.
func resolveOutputLabels(recipe *types.Recipe, nodeID string, node types.IRNode, warn func(string)) map[string]string {
	if recipe == nil {
		return nil
	}
	if len(recipe.Spec.OutputLabels) == 0 && len(recipe.Spec.OutputComponentLabels) == 0 {
		return nil
	}
	labels := make(map[string]string, len(recipe.Spec.OutputLabels)+len(recipe.Spec.OutputComponentLabels))
	for k, v := range recipe.Spec.OutputLabels {
		labels[k] = v
	}
	// Author order, which is what a list preserves. Load-time validation keeps a
	// promoted key out of the literal block, so no promoted entry can overwrite a
	// literal one and the two merges cannot race for a key.
	for _, key := range recipe.Spec.OutputComponentLabels {
		value, declared := node.Labels.Get(key)
		if !declared {
			if warn != nil {
				warn(fmt.Sprintf(
					"node %q: recipe %q promotes component label %q, which component %q does not declare; no label is stamped for it",
					nodeID, recipe.Metadata.Name, key, node.Component))
			}
			continue
		}
		labels[key] = value
	}
	return labels
}
