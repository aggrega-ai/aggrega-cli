// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// buildAnnotationBuckets returns the three independent annotation buckets
// for one node — the IR's structural answer to "annotations come from
// distinct authoring surfaces and must not be merged into a single
// annotation map."
//
// Bucket sources:
//
//   - catalog (CatalogAnnotations) — the Component catalog entity's
//     metadata.annotations. Authored by the Architect at the catalog
//     layer; immutable from the Developer's and Operator's perspective.
//   - release (ReleaseAnnotations) — the Release boundary annotations,
//     already cascaded down to the per-node level by the resolver and
//     surfaced via BoundaryInfra.Annotations. Authored by the Operator.
//   - aggregate (AggregateAnnotations) — reserved. **Empty today.**
//     The schema bans `annotations:` on Aggregate ComponentInstance
//     blocks, so the Aggregate offers no per-instance annotation
//     authoring surface for this bucket to draw from. The bucket exists
//     in the return signature to keep the IRNode.AggregateAnnotations
//     field populated symmetrically with the other two; if a future
//     authoring surface (per-instance subsystem-style annotation
//     cascading) is introduced, this is where its read would land.
//     The inline comment in the body marks the future-extension point.
func buildAnnotationBuckets(
	nodeID string,
	compType string,
	catalogLookup resolver.CatalogLookup,
	boundaryInfra map[string]*resolver.BoundaryInfra,
) (catalog, release, aggregate types.OrderedMap[string]) {
	// Catalog annotations
	if catalogLookup.Component != nil {
		catComp, found := catalogLookup.Component(compType)
		if found && catComp.Metadata.Annotations.Len() > 0 {
			catalog = copyOrderedStringMap(catComp.Metadata.Annotations)
		}
	}

	// Release annotations (from boundary infra, already cascaded)
	if bi, ok := boundaryInfra[nodeID]; ok && bi != nil && bi.Annotations.Len() > 0 {
		release = copyOrderedStringMap(bi.Annotations)
	}

	// Aggregate annotations: reserved for future use.
	// The ComponentInstance type does not currently have an Annotations field.
	// When it is added, populate here.

	return catalog, release, aggregate
}

// buildCatalogMetadata extracts the enterprise-metadata suite (license,
// repository, home, keywords, deprecated, maintainers) from a catalog
// Component's metadata into a *types.CatalogMetadata for embedding in the
// IRNode.
//
// The conditional-return pattern at the bottom of the function — return
// nil when every field is empty — is deliberate: the caller writes
// IRNode.CatalogMetadata = buildCatalogMetadata(...), and the field's
// JSON tag carries `omitempty`. Returning the zero-valued struct would
// emit an empty `{}` object on every node that lacks enterprise
// metadata; returning nil collapses the field out of IR JSON entirely.
// The same input therefore yields the same compact output, which keeps
// the IR diff stable across runs and avoids visual noise in
// `aggrega ir --blueprint` output.
func buildCatalogMetadata(compType string, catalogLookup resolver.CatalogLookup) *types.CatalogMetadata {
	if catalogLookup.Component == nil {
		return nil
	}
	catComp, found := catalogLookup.Component(compType)
	if !found {
		return nil
	}
	meta := &types.CatalogMetadata{
		License:    catComp.Metadata.License,
		Repository: catComp.Metadata.Repository,
		Home:       catComp.Metadata.Home,
		Keywords:   catComp.Metadata.Keywords,
		Deprecated: catComp.Metadata.Deprecated,
	}
	if len(catComp.Metadata.Maintainers) > 0 {
		meta.Maintainers = catComp.Metadata.Maintainers
	}
	// Only return if there's actual content
	if meta.License == "" && meta.Repository == "" && meta.Home == "" &&
		len(meta.Keywords) == 0 && !meta.Deprecated && len(meta.Maintainers) == 0 {
		return nil
	}
	return meta
}

func copyOrderedStringMap(m types.OrderedMap[string]) types.OrderedMap[string] {
	out := types.OrderedMap[string]{}
	for k, v := range m.All() {
		out.Set(k, v)
	}
	return out
}
