// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// buildAnnotationBuckets assembles the three cascading annotation origins for a node.
// - CatalogAnnotations: from the Component catalog entity's metadata.annotations
// - ReleaseAnnotations: from Release boundary annotations (cascaded by resolver)
// - AggregateAnnotations: from the Aggregate ComponentInstance (currently not supported in types, reserved)
func buildAnnotationBuckets(
	nodeID string,
	compType string,
	catalogLookup resolver.CatalogLookup,
	boundaryInfra map[string]*resolver.BoundaryInfra,
) (catalog, release, aggregate map[string]string) {
	// Catalog annotations
	if catalogLookup.Component != nil {
		catComp, found := catalogLookup.Component(compType)
		if found && len(catComp.Metadata.Annotations) > 0 {
			catalog = copyStringMap(catComp.Metadata.Annotations)
		}
	}

	// Release annotations (from boundary infra, already cascaded)
	if bi, ok := boundaryInfra[nodeID]; ok && bi != nil && len(bi.Annotations) > 0 {
		release = copyStringMap(bi.Annotations)
	}

	// Aggregate annotations: reserved for future use.
	// The ComponentInstance type does not currently have an Annotations field.
	// When it is added, populate here.

	return catalog, release, aggregate
}

// buildCatalogMetadata extracts the enterprise metadata suite from a catalog Component.
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

func copyStringMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
