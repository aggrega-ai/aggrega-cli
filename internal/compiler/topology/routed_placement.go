// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package topology

import (
	"encoding/json"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ModuleLoads is the sub-module loading of one build, shared by the two walks of
// the composition that build makes: the walk that places a routed module's
// components under its routing entry before resolution, and the flattening that
// builds the graph. Each walk calls the build's [SubAggregateLoader] through it,
// and a document the placement walk loaded is handed to the flattening rather
// than loaded again, so each sub-module a composition imports is loaded once per
// build, through the one loader flattening has always used.
//
// The hand-over is per import rather than per module type. Two imports of one
// type under the same parameters are two loads, each handed on once, so the two
// prefixes never share a document — exactly as two loads with no placement walk
// in front of them never did.
//
// A nil *ModuleLoads is the build with no loader: it loads nothing, places
// nothing, and its [ModuleLoads.Loader] is nil, which disables sub-module
// loading in flattening as a nil loader always has.
type ModuleLoads struct {
	load SubAggregateLoader
	// loaded holds, per import key, the documents the placement walk loaded and
	// flattening has not yet taken, in the order they were loaded. It is read by
	// lookup only.
	loaded map[string][]moduleLoad
}

// moduleLoad is one result of the build's loader, kept for hand-over.
type moduleLoad struct {
	document   *types.Aggregate
	sourceFile string
	parameters map[string]any
}

// NewModuleLoads returns the sub-module loading of one build over load, or nil
// when load is nil.
func NewModuleLoads(load SubAggregateLoader) *ModuleLoads {
	if load == nil {
		return nil
	}
	return &ModuleLoads{load: load, loaded: map[string][]moduleLoad{}}
}

// Loader returns the loader flattening calls: it takes a document the placement
// walk already loaded for the same import when there is one, and calls the
// build's loader otherwise. A load that failed was never kept, so flattening
// meets the same failure at the same import.
func (m *ModuleLoads) Loader() SubAggregateLoader {
	if m == nil {
		return nil
	}
	return func(typeName string, parentParams map[string]any) (*types.Aggregate, string, map[string]any, error) {
		key, keyed := moduleLoadKey(typeName, parentParams)
		if keyed {
			if pending := m.loaded[key]; len(pending) > 0 {
				m.loaded[key] = pending[1:]
				return pending[0].document, pending[0].sourceFile, pending[0].parameters, nil
			}
		}
		return m.load(typeName, parentParams)
	}
}

// recordingLoader is the loader the placement walk calls: it calls the build's
// loader and keeps each document it returns for flattening to take.
func (m *ModuleLoads) recordingLoader() SubAggregateLoader {
	return func(typeName string, parentParams map[string]any) (*types.Aggregate, string, map[string]any, error) {
		document, sourceFile, parameters, err := m.load(typeName, parentParams)
		if err != nil {
			return nil, "", nil, err
		}
		if key, keyed := moduleLoadKey(typeName, parentParams); keyed {
			m.loaded[key] = append(m.loaded[key], moduleLoad{document: document, sourceFile: sourceFile, parameters: parameters})
		}
		return document, sourceFile, parameters, nil
	}
}

// moduleLoadKey identifies one import's load by the module type and the
// parameters the importing document supplies. The parameters are encoded with
// their keys sorted, so equal fulfilments key equally. A fulfilment that cannot
// be encoded is not keyed, and that import is simply loaded again.
func moduleLoadKey(typeName string, parentParams map[string]any) (string, bool) {
	encoded, err := json.Marshal(parentParams)
	if err != nil {
		return "", false
	}
	return typeName + "\x00" + string(encoded), true
}

// PlaceRoutedComponents places, under each of the Release's routing entries, an
// empty entry for every component of its module the entry does not name, keyed
// by the local name the component carries inside the module and in the order
// the module declares it, after the entries the author wrote.
//
// A routing entry places every component of its module, whether it names all of
// them, some or none; until the module's document is loaded, nothing but that
// entry says so for the ones it does not name. Writing the entries the author
// could have written makes every spelling one: every walk of the boundary tree —
// the annotation-transform role and the annotation-driven expansion among them —
// reaches a routed component through the routing entry, under the id the graph
// gives it, whichever spelling the author chose. An empty entry configures
// nothing, so a component placed this way renders as it does when the author
// names it with an empty body.
//
// The scope is exactly the components no entry names:
//
//   - An entry the author wrote is kept as written, in its place; the entries
//     placed beside it follow it.
//   - A module nested inside a routed module is placed by the outer entry. Where
//     the outer entry carries a routing entry for it, that entry is treated by the
//     same rule; where it carries none, one is written and filled, so the nested
//     module's components are reached under their nested id ("data.inner.store").
//   - A component the Release already names directly under a boundary, by its
//     qualified id, is not placed a second time.
//   - A component or module a false `active` toggle removed was never composed and
//     is not placed.
//
// The placement is made in the Release the build holds in memory; the authored
// document is not written. It reads the composition through the flattening
// walk itself, run over the Aggregate as parsed, with the build's loader, so the
// components placed are exactly the ones flattening composes. A Release that
// routes no module asks for no walk and is left untouched. Where the walk fails —
// a module that does not load, an inclusion cycle — nothing is placed: the same
// failure halts the flattening that follows, at the point it always has.
func (m *ModuleLoads) PlaceRoutedComponents(na types.NormalizedAggregate, release *types.Release) {
	if m == nil || release == nil || !routesAnyModule(release.Spec.Boundaries) {
		return
	}
	composed, err := flattenAggregate(na, "", "", "", m.recordingLoader(), map[string]bool{}, 0, nil)
	if err != nil {
		return
	}
	placeRoutedComponents(release, composed)
}

// routesAnyModule reports whether any boundary, at any depth, carries a routing
// entry.
func routesAnyModule(boundaries types.OrderedMap[types.Boundary]) bool {
	for _, b := range boundaries.All() {
		if b.Aggregates.Len() > 0 || routesAnyModule(b.Boundaries) {
			return true
		}
	}
	return false
}

// routedComposition is what placement reads of a flattened composition: per
// module prefix, the local names of the components the module itself contributes
// and the local names of the modules nested directly inside it, each in
// composition order. Both maps are read by lookup only.
type routedComposition struct {
	components map[string][]string
	nested     map[string][]string
	// declared is every id the Release already names, on either axis of any
	// boundary.
	declared map[string]bool
}

// placeRoutedComponents writes the placement [ModuleLoads.PlaceRoutedComponents]
// describes into release, from the composition composed.
func placeRoutedComponents(release *types.Release, composed *flattenResult) {
	c := routedComposition{
		components: map[string][]string{},
		nested:     map[string][]string{},
		declared:   map[string]bool{},
	}
	for id, node := range composed.Nodes.All() {
		if node.SourceAggregate == "" {
			continue
		}
		c.components[node.SourceAggregate] = append(c.components[node.SourceAggregate],
			strings.TrimPrefix(id, node.SourceAggregate+"."))
	}
	for _, module := range composed.Modules {
		parent := types.QualifiedIDModule(module.Path)
		c.nested[parent] = append(c.nested[parent], strings.TrimPrefix(module.Path, parent+"."))
	}
	collectDeclared(release.Spec.Boundaries, c.declared)
	release.Spec.Boundaries = placeInBoundaries(release.Spec.Boundaries, c)
}

// collectDeclared records every id the boundaries declare, on either axis.
func collectDeclared(boundaries types.OrderedMap[types.Boundary], declared map[string]bool) {
	for _, b := range boundaries.All() {
		for _, decl := range types.BoundaryComponentDeclarations(b) {
			declared[decl.ID] = true
		}
		collectDeclared(b.Boundaries, declared)
	}
}

// placeInBoundaries fills the routing entries of every boundary in the tree and
// returns the tree with each boundary written back under its own key, so the
// author order of boundaries and of routing entries is unchanged.
func placeInBoundaries(boundaries types.OrderedMap[types.Boundary], c routedComposition) types.OrderedMap[types.Boundary] {
	for name, b := range boundaries.All() {
		for module, route := range b.Aggregates.All() {
			b.Aggregates.Set(module, placeInRoute(route, module, c))
		}
		b.Boundaries = placeInBoundaries(b.Boundaries, c)
		boundaries.Set(name, b)
	}
	return boundaries
}

// placeInRoute fills one routing entry for the module at prefix, then the entries
// of the modules nested inside it, and returns the entry.
func placeInRoute(route types.AggregateRouting, prefix string, c routedComposition) types.AggregateRouting {
	for _, local := range c.components[prefix] {
		// declared holds every id the Release names on either axis, the entries
		// this routing entry itself names among them, so an authored entry is
		// never replaced and a component named directly is not placed twice.
		if c.declared[types.JoinQualifiedID(prefix, local)] {
			continue
		}
		route.Components.Set(local, types.BoundaryComponent{})
	}
	for _, child := range c.nested[prefix] {
		childRoute, authored := route.Aggregates.Get(child)
		childRoute = placeInRoute(childRoute, types.JoinQualifiedID(prefix, child), c)
		if authored || childRoute.Components.Len() > 0 || childRoute.Aggregates.Len() > 0 {
			route.Aggregates.Set(child, childRoute)
		}
	}
	return route
}
