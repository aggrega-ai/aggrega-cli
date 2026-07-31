// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// BoundaryInfra is the per-component context produced by walking the Release
// boundary tree. Downstream phases (renderer, IR builder) look up a component's
// boundary context by component instance name; the keying contract is enforced
// by WireBoundaryInfra returning a map keyed by the leaf component name.
//
// A component belongs to exactly one boundary — a name declared in more than one
// is rejected at Release load — so the keying is unambiguous by construction and
// no per-name arbitration happens here.
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
	Infra types.OrderedMap[any] // physical sizing from boundary_component.infra

	// Annotations is the renderer-facing Scalar projection of the
	// post-cascade annotation set for this component scope. Only
	// scalar annotation values surface here; structured annotation
	// values are excluded by construction (they participate in the
	// cascade but flow through Feature expansion rather than
	// template substitution). Consumers needing the full
	// AnnotationValue shape — including structured values for
	// per-subkey routing or attribution — read fresh from the
	// authoring types at rr.Release.Spec.Boundaries.<...>.Annotations
	// rather than from this field. The annotation-expansion phase
	// does exactly that.
	Annotations types.OrderedMap[string]

	BoundaryPath string // e.g. "aws-prod/eks-main/payments"
	Directory    string // resolved output directory
	SubDirectory string // resolved sub-directory
	SyncWave     int    // deployment wave from the containing boundary's syncWave (default 0)
	Synthesized  bool   // true when the containing boundary is the compiler-synthesized default (routes output to dist/ flat)
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
// Components may appear at any depth in the boundary tree, but each name appears
// at exactly one boundary: a name declared in more than one is rejected at
// Release load, so the walk never overwrites a perComponent entry.
//
// Returns (nil, nil) when release is nil.
func WireBoundaryInfra(nr types.NormalizedRelease) (map[string]*BoundaryInfra, map[string]types.OrderedMap[string]) {
	release := nr.Release()
	if release == nil {
		return nil, nil
	}
	perComponent := map[string]*BoundaryInfra{}
	perBoundary := map[string]types.OrderedMap[string]{}
	for bName, boundary := range release.Spec.Boundaries.All() {
		walkBoundary(bName, "", boundary, types.OrderedMap[*types.AnnotationValue]{}, perComponent, perBoundary)
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
func walkBoundary(name, parentPath string, b types.Boundary, parentAnnotations types.OrderedMap[*types.AnnotationValue], perComponent map[string]*BoundaryInfra, perBoundary map[string]types.OrderedMap[string]) {
	currentPath := name
	if parentPath != "" {
		currentPath = parentPath + "/" + name
	}

	// Merge annotations under the AnnotationValue sum-type cascade:
	// parent cascades down, boundary's own annotations override.
	// IsEmpty entries on the overlay opt out of the inherited binding
	// wholesale; structured overlays per-subkey-merge with structured
	// parents; shape mismatches replace. The merge primitive handles
	// all four cases.
	mergedAnnotations := mergeAnnotationValueMaps(parentAnnotations, b.Annotations)

	// Record the boundary-level cascade BEFORE per-component overrides.
	// The renderer-facing surface consumes the Scalar-projection — only
	// scalar entries surface as template substitution; structured
	// annotations flow through Feature expansion only and do not enter
	// the .parent_boundary.annotations / .context.annotations.* render
	// context.
	perBoundary[currentPath] = types.ScalarOnly(mergedAnnotations)

	// Collect components in this boundary
	for compName, comp := range b.Components.All() {
		// BoundaryComponent.Annotations override the boundary chain per key.
		compAnnotations := mergeAnnotationValueMaps(mergedAnnotations, comp.Annotations)
		bi := &BoundaryInfra{
			Infra:        comp.Infra,
			Annotations:  types.ScalarOnly(compAnnotations),
			BoundaryPath: currentPath,
			Directory:    b.Directory,
			SubDirectory: resolveSubDirectory(b.SubDirectory, comp.SubDirectory),
			SyncWave:     b.SyncWave,
			Synthesized:  b.Synthesized,
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

// mergeAnnotationValueMaps composes the annotation cascade: base is
// the parent scope's effective set, overlay is the current scope's
// local annotations. The merge applies four rules per overlay entry,
// in author order:
//
//  1. Overlay value is empty (nil pointer OR non-nil pointer to an
//     AnnotationValue with both fields empty — the third state the
//     defensive UnmarshalYAML null branch can produce) → delete the
//     key from the inherited result. Whole-value opt-out. The
//     belt-and-suspenders predicate is [types.AnnotationValue.IsEmpty]
//     so the merge stays correct against future integration changes
//     in the unmarshaller.
//  2. Overlay is Scalar OR inherited is Scalar (shape mismatch or
//     both-scalar) → overlay replaces the inherited entry wholesale.
//     Scalar and Structured carry different shapes; no merge is
//     attempted across them, and the closer cascade level wins.
//  3. Both overlay and inherited are Structured → per-subkey deep
//     merge: union of subkeys in author order; child subkey nil
//     deletes the merged subkey (per-subkey opt-out); child subkey
//     non-nil overwrites the inherited subkey; subkeys present only
//     in the inherited side stand unchanged.
//  4. Key absent from base → overlay introduces the entry (new key).
//
// The output's key order follows base author order with overlay-only
// keys appended in their overlay author order — consistent with the
// "child appends after base" iteration. Same input produces
// byte-identical output across runs.
func mergeAnnotationValueMaps(base, overlay types.OrderedMap[*types.AnnotationValue]) types.OrderedMap[*types.AnnotationValue] {
	result := types.OrderedMap[*types.AnnotationValue]{}
	for k, v := range base.All() {
		result.Set(k, v)
	}
	for k, overlayValue := range overlay.All() {
		if overlayValue.IsEmpty() {
			result.Delete(k)
			continue
		}
		inherited, hasInherited := result.Get(k)
		if !hasInherited || inherited == nil || inherited.IsScalar() || overlayValue.IsScalar() {
			result.Set(k, overlayValue)
			continue
		}
		// Both inherited and overlay are Structured: per-subkey deep merge.
		merged := types.OrderedMap[any]{}
		for sk, sv := range inherited.Structured.All() {
			merged.Set(sk, sv)
		}
		for sk, sv := range overlayValue.Structured.All() {
			if sv == nil {
				merged.Delete(sk)
				continue
			}
			merged.Set(sk, sv)
		}
		result.Set(k, &types.AnnotationValue{Structured: merged})
	}
	return result
}

// flattenAnnotationValueMap materializes the post-cascade resolved
// annotation set for expansion-side consumers. Nil-pointer entries
// (whole-value opt-out signals already exercised through the merge
// chain) are filtered; non-nil entries are returned as
// AnnotationValue values (pointer indirection collapsed) preserving
// their Scalar / Structured shape. Author order is preserved through
// the filter.
//
// The returned OrderedMap[AnnotationValue] is the contract surface
// the annotation-expansion phase consumes. Render-context template
// substitution sites consume the parallel string-projection via
// [types.ScalarOnly] applied to the pre-flatten map — structured
// values flow through expansion only, not through template
// substrate.
func flattenAnnotationValueMap(m types.OrderedMap[*types.AnnotationValue]) types.OrderedMap[types.AnnotationValue] {
	result := types.OrderedMap[types.AnnotationValue]{}
	for k, v := range m.All() {
		if v.IsEmpty() {
			continue
		}
		result.Set(k, *v)
	}
	return result
}
