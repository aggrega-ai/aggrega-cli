// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import "strings"

// A boundary names the components it holds on two axes, and both mean the same
// thing: a component this boundary contains.
//
// [Boundary.Components] names them one at a time, under the id each carries in
// the flattened graph — so a component belonging to a sub-module is written with
// its qualified dotted id. [Boundary.Aggregates] routes a whole sub-module in at
// once, and names a component — when it names one at all — by the local name it
// carries inside that module, because inside the module that is the only name it
// has.
//
// This file is the one place those two spellings are reconciled. One traversal,
// [BoundaryComponentDeclarations], enumerates what the boundary declares; every
// other function here is a fold of it, so no consumer has to know a second axis
// exists and none can disagree with another about which components a boundary
// holds. The axes stay separate in the document: nothing here rewrites what the
// author wrote, so a routing entry remains the record of where a component was
// declared — which is what [BoundaryComponentLocator] reads to point a diagnostic
// at the key the author actually typed.

// JoinQualifiedID composes the dotted id a component carries in the flattened
// graph from the prefix of the sub-module that contributes it and its own local
// name: "store" for a component of the root, "data.store" one module in,
// "data.inner.store" two.
//
// It is the single spelling of that composition. The Release's routed axis and
// the flattening that produces the graph both build ids with it, so a routed
// entry and the node it addresses cannot come out spelled differently — if they
// could, a routed payload would silently miss its node.
//
// [JoinSubsystemPath] applies the same formula to a different coordinate — the
// subsystem path inside one Aggregate — and is deliberately kept separate: the
// two would have to change independently if either coordinate's separator ever
// did.
func JoinQualifiedID(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// CutQualifiedID splits a dotted id at its first separator into the name of the
// outermost sub-module it passes into and the rest of the id inside that module:
// "data.store" gives "data" and "store", "data.inner.store" gives "data" and
// "inner.store". It reports false for an id naming no module — a component's own
// local name — which is returned whole as the first result.
//
// It is the inverse of [JoinQualifiedID] one level at a time, and it lives beside
// it so the separator the two agree on is spelled in one file.
func CutQualifiedID(id string) (module, rest string, ok bool) {
	return strings.Cut(id, ".")
}

// QualifiedIDModule returns the prefix of the sub-module a dotted id sits in —
// everything before its last separator — and the empty prefix for an id naming
// a component of the root: "data.inner.store" gives "data.inner", "store" gives
// "". A local name carries no separator, so the last one always closes the
// module's prefix.
//
// It is the inverse of [JoinQualifiedID] from the other end, kept beside it for
// the reason [CutQualifiedID] is.
func QualifiedIDModule(id string) string {
	if i := strings.LastIndex(id, "."); i >= 0 {
		return id[:i]
	}
	return ""
}

// IsUnderModulePrefix reports whether id names something inside the sub-module
// at prefix — the module itself, or anything nested below it.
//
// The comparison is on whole dotted segments rather than on raw characters, so
// "database" is not treated as sitting inside "data".
func IsUnderModulePrefix(id, prefix string) bool {
	if prefix == "" {
		return true
	}
	return id == prefix || strings.HasPrefix(id, prefix+".")
}

// BoundaryComponentDeclaration is one component entry a boundary declares, on
// whichever of its two axes declared it.
//
// ID is the id the component carries in the flattened graph. Locator is the key
// path within the boundary, exactly as authored, so a diagnostic or a provenance
// record can name the line rather than the derived id.
type BoundaryComponentDeclaration struct {
	ID        string
	Locator   []string
	Component BoundaryComponent
}

// BoundaryComponentDeclarations returns every component entry the boundary
// declares, in author order: the boundary's own entries first, then each routed
// sub-module in the order the Release routes them, each module's own components
// before the modules nested inside it.
//
// Collisions are NOT reconciled here — two entries resolving to one id are both
// returned. That is deliberate and is the reason this traversal is separate from
// [BoundaryComponents]: a component named directly and the same component named
// again through a routing entry is a document that places one node twice, and a
// view that folded the two together would hide exactly the ambiguity a caller
// needs to reject.
func BoundaryComponentDeclarations(b Boundary) []BoundaryComponentDeclaration {
	out := make([]BoundaryComponentDeclaration, 0, b.Components.Len())
	for name, comp := range b.Components.All() {
		out = append(out, BoundaryComponentDeclaration{
			ID:        name,
			Locator:   []string{"components", name},
			Component: comp,
		})
	}
	collectRoutedDeclarations(&out, b.Aggregates, "", nil)
	return out
}

func collectRoutedDeclarations(out *[]BoundaryComponentDeclaration, routes OrderedMap[AggregateRouting], prefix string, locatorPrefix []string) {
	for module, route := range routes.All() {
		modulePrefix := JoinQualifiedID(prefix, module)
		moduleLocator := append(append([]string{}, locatorPrefix...), "aggregates", module)
		for local, comp := range route.Components.All() {
			*out = append(*out, BoundaryComponentDeclaration{
				ID:        JoinQualifiedID(modulePrefix, local),
				Locator:   append(append([]string{}, moduleLocator...), "components", local),
				Component: comp,
			})
		}
		collectRoutedDeclarations(out, route.Aggregates, modulePrefix, moduleLocator)
	}
}

// BoundaryComponents returns every component the boundary holds, on both axes,
// keyed by the id each carries in the flattened graph and in author order.
//
// A boundary that routes nothing returns its own map unchanged, so the common
// shape costs no allocation and no copy.
//
// Two entries resolving to one id fold to the later one. Nothing relies on which
// wins: the shape is rejected when the Release loads, and this fold runs behind
// that rejection. [BoundaryComponentDeclarations] is what sees the collision.
func BoundaryComponents(b Boundary) OrderedMap[BoundaryComponent] {
	if b.Aggregates.Len() == 0 {
		return b.Components
	}
	out := OrderedMap[BoundaryComponent]{}
	for _, decl := range BoundaryComponentDeclarations(b) {
		out.Set(decl.ID, decl.Component)
	}
	return out
}

// BoundaryRoutedModules returns the dotted prefix of every sub-module the
// boundary routes, in author order, outer module before the modules nested inside
// it.
//
// A prefix appears whether or not the routing entry names any component: naming
// none is how a Release routes a whole module without restating its contents, and
// that is the routing entry's primary shape rather than a degenerate one. It is
// therefore the only record of a placement whose components cannot be enumerated
// until the module's own document is loaded.
func BoundaryRoutedModules(b Boundary) []string {
	if b.Aggregates.Len() == 0 {
		return nil
	}
	var out []string
	collectRoutedModules(&out, b.Aggregates, "")
	return out
}

func collectRoutedModules(out *[]string, routes OrderedMap[AggregateRouting], prefix string) {
	for module, route := range routes.All() {
		modulePrefix := JoinQualifiedID(prefix, module)
		*out = append(*out, modulePrefix)
		collectRoutedModules(out, route.Aggregates, modulePrefix)
	}
}

// BoundaryComponentLocator returns the authoring key path at which the boundary
// declares name, relative to the boundary itself: the segments a diagnostic or a
// provenance record joins to point the author at the entry they actually wrote.
//
// A component the boundary names directly locates at "components" and its id. A
// routed one locates through the routing entries that reach it, and under the
// LOCAL name it was written with — "aggregates", "data", "components",
// "consumer" — because that is the key present in the document. Spelling it with
// the qualified id would name a key the author never wrote and, on the routed
// axis, one the Release schema does not accept.
//
// Returns the direct spelling when name is on neither axis, which is what a
// caller recording a component it is in the act of introducing needs.
func BoundaryComponentLocator(b Boundary, name string) []string {
	if b.Aggregates.Len() == 0 {
		return []string{"components", name}
	}
	if _, ok := b.Components.Get(name); ok {
		return []string{"components", name}
	}
	for _, decl := range BoundaryComponentDeclarations(b) {
		if decl.ID == name {
			return decl.Locator
		}
	}
	return []string{"components", name}
}

// SetBoundaryComponent writes comp back into whichever axis of the boundary
// declares name, and is the counterpart of [BoundaryComponents]: a consumer that
// reads a component through the reconciled view and mutates it writes it back
// here, so the mutation lands on the entry the author wrote.
//
// Without it, a component read from the routed axis would be written to the
// boundary's own axis under its qualified id, leaving the same component declared
// twice in one document — once as the author wrote it and once as a compiler
// artifact — with every later reader free to pick either.
//
// It navigates the routed axis itself rather than folding
// [BoundaryComponentDeclarations]: a write has to reach the container that holds
// the entry, which a flattened enumeration does not hand back. The two stay
// consistent because both compose ids with [JoinQualifiedID] and nothing else.
//
// A name on neither axis is written to the boundary's own axis, which is where a
// component the caller is introducing belongs.
func SetBoundaryComponent(b *Boundary, name string, comp BoundaryComponent) {
	if b == nil {
		return
	}
	if _, ok := b.Components.Get(name); ok {
		b.Components.Set(name, comp)
		return
	}
	if setRoutedComponent(&b.Aggregates, "", name, comp) {
		return
	}
	b.Components.Set(name, comp)
}

func setRoutedComponent(routes *OrderedMap[AggregateRouting], prefix, name string, comp BoundaryComponent) bool {
	for module, route := range routes.All() {
		modulePrefix := JoinQualifiedID(prefix, module)
		for local := range route.Components.All() {
			if JoinQualifiedID(modulePrefix, local) != name {
				continue
			}
			// OrderedMap hands back values by value, so the routing entry held here
			// is a copy; the mutated copy is written back under its own key. Setting
			// an existing key leaves the key order untouched, so the iteration this
			// sits inside is unaffected.
			route.Components.Set(local, comp)
			routes.Set(module, route)
			return true
		}
		if setRoutedComponent(&route.Aggregates, modulePrefix, name, comp) {
			routes.Set(module, route)
			return true
		}
	}
	return false
}
