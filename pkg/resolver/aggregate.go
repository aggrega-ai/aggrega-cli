// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// WireAggregateAnnotations walks the Aggregate's subsystem and
// component tree and computes the per-component effective annotation
// set, applying the cascade: subsystem annotations propagate to nested
// subsystems and to contained components; closer-level overrides
// parent per-key; whole-value nils opt out of inherited keys;
// structured-value subkey nils opt out per-subkey.
//
// Returns a map from canonical component path to the post-cascade
// resolved OrderedMap[string]. Canonical component paths use dotted
// notation reflecting the authoring tree structure:
//
//   - Subsystem-contained component:     subsystems.<sub>.components.<name>
//   - Nested-subsystem component:        subsystems.<outer>.subsystems.<inner>.components.<name>
//
// A component authored at the top-level `.spec.components` surface is reached
// through the synthesized default subsystem and keys at
// subsystems.default.components.<name>.
//
// The returned map values are the renderer-facing Scalar-projection of
// the post-flatten cascade: only scalar entries surface in the
// projection so consumers (template substitution, etc.) see a
// string-keyed string-valued map. Structured annotations flow through
// the parallel expansion cascade in annotation_expansion.go and
// synthesize Feature attachments rather than entering the render
// context here.
//
// Returns nil when agg is nil.
func WireAggregateAnnotations(na types.NormalizedAggregate) map[string]types.OrderedMap[string] {
	agg := na.Aggregate()
	if agg == nil {
		return nil
	}
	perComponent := map[string]types.OrderedMap[string]{}

	// The accumulator is the cascade: the AnnotationValue sum-type map is carried
	// through the descent so structured-value subkey opt-outs at any scope keep
	// suppressing inherited subkeys for deeper scopes. Scalar-projecting to
	// OrderedMap[string] happens only at the renderer-facing leaf, where the
	// per-component effective set is finalized for template substitution.
	//
	// No visitor callback fails, so the walk cannot.
	_ = types.WalkInstanceTree(agg.Spec.Subsystems, types.SubsystemTree(), "", types.OrderedMap[*types.AnnotationValue]{},
		types.InstanceTreeVisitor[types.Subsystem, types.ComponentInstance, types.OrderedMap[*types.AnnotationValue]]{
			Path: aggregateAnnotationScopePath,
			// Merge the inherited cascade with this subsystem's own annotations per the
			// AnnotationValue rules: whole-value nils opt out wholesale; structured +
			// structured deep-merges per subkey with per-subkey nil opt-out; shape
			// mismatches replace.
			Enter: func(sub types.Subsystem, _, _ string, inherited types.OrderedMap[*types.AnnotationValue]) types.OrderedMap[*types.AnnotationValue] {
				return mergeAnnotationValueMaps(inherited, sub.Annotations)
			},
			// Merge the component's own annotations on top of the subsystem-level
			// cascade, then project to the OrderedMap[string] downstream consumers use.
			Visit: func(comp types.ComponentInstance, name, path string, _ bool, cascade types.OrderedMap[*types.AnnotationValue]) error {
				compAnnotations := mergeAnnotationValueMaps(cascade, comp.Annotations)
				perComponent[path+".components."+name] = types.ScalarOnly(compAnnotations)
				return nil
			},
		})

	return perComponent
}

// aggregateAnnotationScopePath composes the canonical authoring path of one
// annotation scope in the Aggregate's subsystem tree: "subsystems.<s>" at the top
// level and "subsystems.<outer>.subsystems.<inner>" below it. The component leaf
// appends ".components.<name>".
//
// It satisfies [types.InstanceTreeVisitor.Path]; the path does not vary with the
// synthesized flag, because a component folded into the synthesized default keys
// at subsystems.default.components.<name> like any other.
func aggregateAnnotationScopePath(parentPath, name string, _ bool) string {
	if parentPath == "" {
		return "subsystems." + name
	}
	return parentPath + ".subsystems." + name
}
