// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// BoundaryInfra holds the collected infra and annotations for one component from the Release boundaries.
type BoundaryInfra struct {
	Infra        types.OrderedMap[interface{}] // physical sizing from boundary_component.infra
	Annotations  types.OrderedMap[string]      // cascaded from boundary.annotations
	BoundaryPath string                        // e.g. "aws-prod/eks-main/payments"
	Directory    string                        // resolved output directory
	SubDirectory string                        // resolved sub-directory
}

// WireBoundaryInfra walks the recursive Release boundaries and collects per-component
// infra maps, boundary annotations, and boundary paths.
// Returns a map keyed by component instance name.
func WireBoundaryInfra(release *types.Release) map[string]*BoundaryInfra {
	if release == nil {
		return nil
	}
	result := map[string]*BoundaryInfra{}
	for bName, boundary := range release.Spec.Boundaries.All() {
		walkBoundary(bName, "", boundary, types.OrderedMap[string]{}, result)
	}
	return result
}

func walkBoundary(name, parentPath string, b types.Boundary, parentAnnotations types.OrderedMap[string], result map[string]*BoundaryInfra) {
	currentPath := name
	if parentPath != "" {
		currentPath = parentPath + "/" + name
	}

	// Merge annotations: parent cascades down, boundary's own annotations override
	mergedAnnotations := mergeStringMaps(parentAnnotations, b.Annotations)

	// Collect components in this boundary
	for compName, comp := range b.Components.All() {
		// BoundaryComponent.Annotations override the boundary chain per key.
		compAnnotations := mergeStringMaps(mergedAnnotations, comp.Annotations)
		bi := &BoundaryInfra{
			Infra:        comp.Infra,
			Annotations:  compAnnotations,
			BoundaryPath: currentPath,
			Directory:    b.Directory,
			SubDirectory: resolveSubDirectory(b.SubDirectory, comp.SubDirectory),
		}
		result[compName] = bi
	}

	// Recurse into nested boundaries
	for childName, childBoundary := range b.Boundaries.All() {
		walkBoundary(childName, currentPath, childBoundary, mergedAnnotations, result)
	}
}

func resolveSubDirectory(boundaryLevel, componentLevel string) string {
	if componentLevel != "" {
		return componentLevel
	}
	return boundaryLevel
}

func mergeStringMaps(base, overlay types.OrderedMap[string]) types.OrderedMap[string] {
	result := types.OrderedMap[string]{}
	for k, v := range base.All() {
		result.Set(k, v)
	}
	for k, v := range overlay.All() {
		result.Set(k, v)
	}
	return result
}
