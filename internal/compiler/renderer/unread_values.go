// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// unreadValueWarnings reports the node's authored values and infra keys that the
// Recipe's valuesMapping never references — a knob an author added and nothing
// consumes, which is a smell rather than a fault, so each one is a warning and
// none of them halts the build.
//
// A static walk cannot tell a deliberate don't-care from a mistake, which is why
// the reading only ever advises. Two bounds follow from what can be read at all:
//
//   - Only an inline valuesMapping is read. A mapping the Recipe imports from a
//     file or a library is not in hand here, so its references cannot be
//     collected and no claim is made about the node's keys.
//   - A mapping that fails to parse yields no reference set. Reporting every key
//     as unread on a parse failure would bury the real diagnostic the evaluation
//     raises for the same template, so nothing is reported instead.
//
// Warnings come back in a fixed order — every values key, then every infra key,
// each group in the order the census returns — so the same input produces the
// same advisory stream on every run.
func unreadValueWarnings(nodeID string, recipe *types.Recipe, values, infra types.OrderedMap[any]) []string {
	if recipe == nil || recipe.Spec.ValuesMapping.Inline == "" {
		return nil
	}
	refs, err := gotmpl.CollectTemplateFieldAccesses(recipe.Spec.ValuesMapping.Inline)
	if err != nil {
		return nil
	}
	// A slice rather than a map: the pillars are reported in this order on every
	// run, which a map's iteration could not promise.
	pillars := []struct {
		root string
		data types.OrderedMap[any]
	}{
		{".values", values},
		{".infra", infra},
	}
	var warnings []string
	for _, p := range pillars {
		for _, k := range gotmpl.UnreadKeys(refs, p.root, p.data) {
			warnings = append(warnings,
				fmt.Sprintf("node %q: unused %s key %q (not referenced by valuesMapping of recipe %q)",
					nodeID, p.root, k, recipe.Metadata.Name))
		}
	}
	return warnings
}
