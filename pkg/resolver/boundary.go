// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// BoundaryInfra is the per-component context produced by walking the Release
// boundary tree. Downstream phases (renderer, IR builder) look up a component's
// boundary context by component instance name; the keying contract is enforced
// by WireBoundaryInfra returning a map keyed by the leaf component name.
//
// A component name appearing in more than one boundary subtree resolves to
// last-write-wins under the OrderedMap iteration order of Release.spec.boundaries.
//
// Annotations carry the full cascaded result: parent boundary annotations
// merged with the current boundary's own annotations, then merged with the
// BoundaryComponent.annotations of the component itself. Each layer overrides
// the previous per key.
//
// BoundaryPath is the slash-separated traversal path to the boundary that
// contained the component (e.g. "aws-prod/eks-main/payments").
//
// Directory and SubDirectory are the output-path hints declared on the
// boundary that contained the component; they do not inherit from parent
// boundaries. SubDirectory follows component-overrides-boundary precedence:
// a non-empty BoundaryComponent.subDirectory wins over the boundary's value.
type BoundaryInfra struct {
	Infra        types.OrderedMap[interface{}] // physical sizing from boundary_component.infra
	Annotations  types.OrderedMap[string]      // cascaded from boundary.annotations
	BoundaryPath string                        // e.g. "aws-prod/eks-main/payments"
	Directory    string                        // resolved output directory
	SubDirectory string                        // resolved sub-directory
}

// WireBoundaryInfra walks the recursive Release boundary tree and produces
// a per-component context map keyed by component instance name.
//
// The walk is depth-first over Release.spec.boundaries. At each boundary:
//
//   - The current boundary's annotations are merged onto the inherited
//     parent annotations (per-key last-write-wins).
//   - Every component declared at the boundary is recorded into the result
//     map. Per-component annotations are merged onto the cascaded boundary
//     annotations as a third layer (per-key last-write-wins).
//   - Nested boundaries are recursed with the cascaded annotations as the
//     inherited input.
//
// Components may appear at any depth in the boundary tree. A component name
// that appears at more than one boundary resolves to last-write-wins under
// the OrderedMap iteration order of the parent boundary's components map and
// the recursive descent order.
//
// Returns nil when release is nil.
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
