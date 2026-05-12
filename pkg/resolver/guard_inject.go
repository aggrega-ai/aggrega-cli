// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// guardInjectKeys is a placeholder for the inject-from-key validator
// against the target catalog entity's provides set. The validator
// extracts the `provides` pillar shape from the target's Spec.Schema
// JSON Schema content and checks that every `from:` key in the
// developer's inject blocks references a published key.
//
// The extractor activates once the load pipeline compiles Spec.Schema
// into JSON Schema (via pkg/compactform) and surfaces the `provides`
// keys as a typed set on the resolved catalog entity. Today the guard
// is a no-op; the call site exists so RunGuards' contract — Step 1
// runs an inject-key check — stays declared even while the extractor
// integration ships in a separate work item.
func guardInjectKeys(agg *types.Aggregate, catalog CatalogLookup) []*ResolverError {
	if agg == nil || catalog.Component == nil {
		return nil
	}
	_ = agg
	_ = catalog
	return nil
}
