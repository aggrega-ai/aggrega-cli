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
//   - Top-level component:               components.<name>
//   - Subsystem-contained component:     subsystems.<sub>.components.<name>
//   - Nested-subsystem component:        subsystems.<outer>.subsystems.<inner>.components.<name>
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

	// Top-level components carry no enclosing subsystem; the parent
	// annotation set entering this scope is empty. Component paths are
	// rooted at the canonical "components." prefix.
	for compName, comp := range agg.Spec.Components.All() {
		merged := mergeAnnotationValueMaps(types.OrderedMap[*types.AnnotationValue]{}, comp.Annotations)
		perComponent["components."+compName] = types.ScalarOnly(merged)
	}

	// Top-level subsystems carry their own annotations plus a per-
	// subsystem subtree. Each subsystem's recursion roots at the
	// canonical "subsystems." prefix; the recursive helper extends the
	// path string as it descends.
	for subName, sub := range agg.Spec.Subsystems.All() {
		walkAggregateSubsystem(subName, "subsystems", sub, types.OrderedMap[*types.AnnotationValue]{}, perComponent)
	}

	return perComponent
}

// walkAggregateSubsystem recurses one Subsystem of the Aggregate tree,
// cascading annotations from outer subsystems into the current one and
// into each contained component, then descending into nested
// subsystems. The cascade carries the AnnotationValue sum-type map
// through recursion so structured-value subkey opt-outs at any scope
// continue to suppress inherited subkeys for deeper scopes;
// Scalar-projecting to OrderedMap[string] happens only at the
// renderer-facing leaf where the per-component effective set is
// finalized for template substitution.
//
// parentPath is the dotted prefix accumulated by the caller — for the
// first-level entry from WireAggregateAnnotations, parentPath is
// "subsystems"; for a recursive call into a nested subsystem, the
// caller appends the current subsystem's name and the bridge
// ".subsystems" before passing parentPath down.
func walkAggregateSubsystem(
	name, parentPath string,
	s types.Subsystem,
	parentAnnotations types.OrderedMap[*types.AnnotationValue],
	perComponent map[string]types.OrderedMap[string],
) {
	// The canonical authoring path for this subsystem: parent prefix +
	// "." + this subsystem's name. The component-path leaf appends
	// ".components.<comp>"; the nested-subsystem recursion appends
	// ".subsystems".
	currentPath := parentPath + "." + name

	// Merge parent annotations with this subsystem's own per the
	// AnnotationValue cascade rules: whole-value nils opt out
	// wholesale; structured + structured deep-merges per subkey with
	// per-subkey nil opt-out; shape mismatches replace.
	mergedAnnotations := mergeAnnotationValueMaps(parentAnnotations, s.Annotations)

	// For each contained component, merge in the component's own
	// annotations on top of the subsystem-level cascade, then project
	// to the post-flatten OrderedMap[string] downstream consumers use.
	// Components are written to perComponent keyed by their canonical
	// dotted path.
	for compName, comp := range s.Components.All() {
		compAnnotations := mergeAnnotationValueMaps(mergedAnnotations, comp.Annotations)
		perComponent[currentPath+".components."+compName] = types.ScalarOnly(compAnnotations)
	}

	// Recurse into nested subsystems. The cascade-level annotation map
	// passed down is the subsystem-merged form so nested scopes can
	// further override or opt-out at any granularity.
	for childName, child := range s.Subsystems.All() {
		walkAggregateSubsystem(childName, currentPath+".subsystems", child, mergedAnnotations, perComponent)
	}
}
