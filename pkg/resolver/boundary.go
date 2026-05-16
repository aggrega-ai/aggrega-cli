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
//
// SyncWave is the deployment wave inherited from the IMMEDIATE containing
// boundary's syncWave field. Default 0 (signed integer; negative permitted).
// Nested boundaries do NOT inherit from outer boundaries — each boundary
// declares its own SyncWave. Operators wanting "all of outer is wave 1,
// including its inner sub-boundary" declare syncWave: 1 on both. The IR
// builder reads bi.SyncWave to populate IRNode.SyncWave per-component.
type BoundaryInfra struct {
	Infra        types.OrderedMap[any]    // physical sizing from boundary_component.infra
	Annotations  types.OrderedMap[string] // cascaded from boundary.annotations
	BoundaryPath string                   // e.g. "aws-prod/eks-main/payments"
	Directory    string                   // resolved output directory
	SubDirectory string                   // resolved sub-directory
	SyncWave     int                      // deployment wave from the containing boundary's syncWave (default 0)
}

// WireBoundaryInfra walks the recursive Release boundary tree and produces
// two outputs:
//
//   - perComponent: a map keyed by component instance name, each entry a
//     full BoundaryInfra struct including the cascaded annotations with
//     BoundaryComponent.Annotations layered on top.
//   - perBoundary: a map keyed by slash-separated boundary path
//     (e.g. "aws-prod/eks-main/payments"), each entry the cascaded
//     annotation set effective at that boundary level — parent boundaries'
//     annotations merged with the boundary's own annotations, but WITHOUT
//     the per-component overrides. Used by orchestrator-time chart-name
//     match checks (a single chart-name applies per assembly path; the
//     check operates at boundary granularity, not per-component).
//
// The walk is depth-first over Release.spec.boundaries. At each boundary:
//
//   - The current boundary's annotations are merged onto the inherited
//     parent annotations (per-key last-write-wins). The merged set is
//     recorded into perBoundary at the current path.
//   - Every component declared at the boundary is recorded into
//     perComponent. Per-component annotations are merged onto the cascaded
//     boundary annotations as a third layer (per-key last-write-wins).
//   - Nested boundaries are recursed with the cascaded annotations as the
//     inherited input.
//
// Components may appear at any depth in the boundary tree. A component name
// that appears at more than one boundary resolves to last-write-wins under
// the OrderedMap iteration order of the parent boundary's components map and
// the recursive descent order.
//
// Returns (nil, nil) when release is nil.
func WireBoundaryInfra(release *types.Release) (map[string]*BoundaryInfra, map[string]types.OrderedMap[string]) {
	if release == nil {
		return nil, nil
	}
	perComponent := map[string]*BoundaryInfra{}
	perBoundary := map[string]types.OrderedMap[string]{}
	for bName, boundary := range release.Spec.Boundaries.All() {
		walkBoundary(bName, "", boundary, types.OrderedMap[*string]{}, perComponent, perBoundary)
	}
	return perComponent, perBoundary
}

// walkBoundary recurses the Release boundary tree, cascading annotations
// from outer boundaries into inner ones and into each boundary's routed
// components. The cascade carries the pointer-tristate so YAML `null`
// can opt-out an inherited annotation; the post-cascade
// [BoundaryInfra.Annotations] and perBoundary maps exposed to downstream
// phases are the flattened OrderedMap[string] form — nil entries are
// filtered out (opt-out applied) and non-nil entries are dereferenced.
func walkBoundary(name, parentPath string, b types.Boundary, parentAnnotations types.OrderedMap[*string], perComponent map[string]*BoundaryInfra, perBoundary map[string]types.OrderedMap[string]) {
	currentPath := name
	if parentPath != "" {
		currentPath = parentPath + "/" + name
	}

	// Merge annotations under pointer-tristate: parent cascades down,
	// boundary's own annotations override (a nil entry from the
	// boundary's overlay overrides a non-nil entry from the parent,
	// encoding opt-out).
	mergedAnnotations := mergePointerStringMaps(parentAnnotations, b.Annotations)

	// Record the boundary-level cascade BEFORE per-component overrides.
	// Used by orchestrator-time chart-name match checks (effective
	// aggrega.ai/chart-name resolves at boundary granularity).
	perBoundary[currentPath] = flattenPointerStringMap(mergedAnnotations)

	// Collect components in this boundary
	for compName, comp := range b.Components.All() {
		// BoundaryComponent.Annotations override the boundary chain per key.
		compAnnotations := mergePointerStringMaps(mergedAnnotations, comp.Annotations)
		bi := &BoundaryInfra{
			Infra:        comp.Infra,
			Annotations:  flattenPointerStringMap(compAnnotations),
			BoundaryPath: currentPath,
			Directory:    b.Directory,
			SubDirectory: resolveSubDirectory(b.SubDirectory, comp.SubDirectory),
			SyncWave:     b.SyncWave,
		}
		perComponent[compName] = bi
	}

	// Recurse into nested boundaries
	for childName, childBoundary := range b.Boundaries.All() {
		walkBoundary(childName, currentPath, childBoundary, mergedAnnotations, perComponent, perBoundary)
	}
}

func resolveSubDirectory(boundaryLevel, componentLevel string) string {
	if componentLevel != "" {
		return componentLevel
	}
	return boundaryLevel
}

// mergePointerStringMaps composes the Release-annotation cascade: base
// is the parent boundary's effective set, overlay is the current
// boundary or component's local annotations. Last-write-wins on the
// entire pointer per key — a nil pointer from overlay overrides a
// non-nil pointer from base (the opt-out signal); a non-nil pointer
// from overlay overrides any value from base; keys present in base but
// absent from overlay survive unchanged (inherit).
func mergePointerStringMaps(base, overlay types.OrderedMap[*string]) types.OrderedMap[*string] {
	result := types.OrderedMap[*string]{}
	for k, v := range base.All() {
		result.Set(k, v)
	}
	for k, v := range overlay.All() {
		result.Set(k, v)
	}
	return result
}

// flattenPointerStringMap materializes the post-cascade resolved
// annotation set: nil-pointer entries are filtered (opt-out applied —
// the inherited binding is removed) and non-nil-pointer entries are
// dereferenced. Author order is preserved through the filter. The
// returned OrderedMap[string] is the contract surface downstream
// phases (IR annotation copy, renderer label injection, orchestrator
// chart-name match) consume.
func flattenPointerStringMap(m types.OrderedMap[*string]) types.OrderedMap[string] {
	result := types.OrderedMap[string]{}
	for k, v := range m.All() {
		if v == nil {
			continue
		}
		result.Set(k, *v)
	}
	return result
}
