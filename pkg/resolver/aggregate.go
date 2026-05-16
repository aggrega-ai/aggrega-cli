// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// WireAggregateAnnotations walks the Aggregate's subsystem and
// component tree and computes the per-component effective annotation
// set, applying the cascade: subsystem annotations propagate to nested
// subsystems and to contained components; closer-level overrides
// parent at per-key granularity; nil-pointer entries (explicit YAML
// null) opt out of inherited keys.
//
// Returns a map from canonical component path to the post-cascade
// resolved OrderedMap[string]. Canonical component paths use dotted
// notation reflecting the authoring tree structure:
//
//   - Top-level component:               components.<name>
//   - Subsystem-contained component:     subsystems.<sub>.components.<name>
//   - Nested-subsystem component:        subsystems.<outer>.subsystems.<inner>.components.<name>
//
// The returned map values are post-flatten OrderedMap[string]: nil
// entries (annotation opt-outs) are filtered; non-nil entries are
// dereferenced. Author order through the cascade carries into the
// resolved map.
//
// Returns nil when agg is nil.
func WireAggregateAnnotations(agg *types.Aggregate) map[string]types.OrderedMap[string] {
	if agg == nil {
		return nil
	}
	perComponent := map[string]types.OrderedMap[string]{}

	// Top-level components carry no enclosing subsystem; the parent
	// annotation set entering this scope is empty. Component paths are
	// rooted at the canonical "components." prefix.
	for compName, comp := range agg.Spec.Components.All() {
		merged := mergePointerStringMaps(types.OrderedMap[*string]{}, comp.Annotations)
		perComponent["components."+compName] = flattenPointerStringMap(merged)
	}

	// Top-level subsystems carry their own annotations plus a per-
	// subsystem subtree. Each subsystem's recursion roots at the
	// canonical "subsystems." prefix; the recursive helper extends the
	// path string as it descends.
	for subName, sub := range agg.Spec.Subsystems.All() {
		walkAggregateSubsystem(subName, "subsystems", sub, types.OrderedMap[*string]{}, perComponent)
	}

	return perComponent
}

// walkAggregateSubsystem recurses one Subsystem of the Aggregate tree,
// cascading annotations from outer subsystems into the current one and
// into each contained component, then descending into nested
// subsystems. The cascade carries the pointer-tristate
// OrderedMap[*string] through recursion so explicit YAML null at any
// scope continues to opt out the inherited annotation for deeper
// scopes; flattening to OrderedMap[string] happens only at the leaf
// write site where the per-component effective set is finalized.
//
// parentPath is the dotted prefix accumulated by the caller — for the
// first-level entry from WireAggregateAnnotations, parentPath is
// "subsystems"; for a recursive call into a nested subsystem, the
// caller appends the current subsystem's name and the bridge
// ".subsystems" before passing parentPath down.
func walkAggregateSubsystem(
	name, parentPath string,
	s types.Subsystem,
	parentAnnotations types.OrderedMap[*string],
	perComponent map[string]types.OrderedMap[string],
) {
	// The canonical authoring path for this subsystem: parent prefix +
	// "." + this subsystem's name. The component-path leaf appends
	// ".components.<comp>"; the nested-subsystem recursion appends
	// ".subsystems".
	currentPath := parentPath + "." + name

	// Merge parent annotations with this subsystem's own. Last-write-
	// wins on the entire pointer per key: a nil entry from this
	// subsystem overrides a non-nil entry from the parent (opt-out
	// signal); a non-nil entry overrides any inherited value; keys
	// absent from this subsystem survive from the parent (inherit).
	mergedAnnotations := mergePointerStringMaps(parentAnnotations, s.Annotations)

	// For each contained component, merge in the component's own
	// annotations on top of the subsystem-level cascade, then flatten
	// to the post-cascade OrderedMap[string] downstream consumers use.
	// Components are written to perComponent keyed by their canonical
	// dotted path.
	for compName, comp := range s.Components.All() {
		compAnnotations := mergePointerStringMaps(mergedAnnotations, comp.Annotations)
		perComponent[currentPath+".components."+compName] = flattenPointerStringMap(compAnnotations)
	}

	// Recurse into nested subsystems. The cascade-level annotation map
	// passed down is the subsystem-merged form (still pointer-tristate)
	// so nested scopes can further override or opt-out.
	for childName, child := range s.Subsystems.All() {
		walkAggregateSubsystem(childName, currentPath+".subsystems", child, mergedAnnotations, perComponent)
	}
}
