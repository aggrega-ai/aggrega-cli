// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// topologyView presents the flattened composition to a check as named facts: the
// node set with the modules that contributed it, each node's placement and
// resolved parameters, and the connection set every origin has contributed to.
//
// It satisfies [constraint.TopologyView], and the compiler holds it to that
// contract at the bottom of this file rather than a comment claiming it.
//
// # The earlier stage is embedded, not re-projected
//
// A stage's view contains the stage it extends, so every fact complete before
// flattening is readable here. Those facts are answered by the presentation the
// resolve already built, embedded and delegated to, rather than re-derived from
// the same documents: one fact has one producer, and two producers of a fact are
// free to disagree about it with nothing comparing them.
//
// The delegation is honest for a node a module contributed, and the reason is
// the accessors' own declarations rather than a convention applied on top of
// them. An earlier-stage fact over the application's own instance list answers
// about the document that list was read from, and its declaration says so: a
// component a module contributes is declared in that module's document, which is
// read only when the composition is flattened, so the earlier fact reports it as
// absent. That is what the flattened facts here exist to answer instead, and
// which of the two a rule wants is a question the rule settles by naming a fact.
// So no accessor in this presentation refuses, and none answers with a value
// projected from the wrong stage.
//
// # Values are handed over uncopied
//
// The contract every view shares makes a fact's value immutable to a check, and
// this value keeps that contract: what an accessor returns aliases what the
// compilation already holds.
type topologyView struct {
	constraint.NormalizedView

	// nodes is the flattened node set, by the identity every keyed fact takes.
	nodes types.OrderedMap[*TopologyNode]

	// connections is each node's socket connection counts, folded once when this
	// value was built so a reader does not walk the edge set per node.
	connections map[string]types.OrderedMap[int]

	// modules is every sub-module the composition brought in, in the order
	// flattening recorded them.
	modules []ModuleDeclaration

	// pruned is every node a false activation toggle removed from the
	// composition. Only ever stores true, so presence is membership.
	pruned types.OrderedMap[bool]

	// contributed is the nodes each module contributed, keyed by the module's
	// dotted prefix and including the nodes its nested modules contributed.
	contributed map[string][]string

	// references is every component entry the deployment's boundaries declare,
	// on both of the axes a boundary names components on, in the order the
	// deployment declares them.
	//
	// It is what the deployment asked for and is deliberately not reconciled
	// against the node set above: an entry addressing an id nothing answers to
	// is exactly the entry a rule over this axis exists to be asked about, so
	// folding the two together here would remove the subject before any rule saw
	// it.
	references []constraint.BoundaryComponentReferenceInstance
}

// newTopologyView presents the composition the build has just flattened.
//
// The products are taken rather than reached for, because a view projects facts
// already produced and never produces one of its own. What it folds here — the
// per-node connection counts and the per-module node lists — is folded once so
// that a walk over one axis does not become a walk over every other.
func newTopologyView(
	flat *flattenResult,
	edges []NormalizedEdge,
	earlier constraint.NormalizedView,
	references []constraint.BoundaryComponentReferenceInstance,
) *topologyView {
	v := &topologyView{
		NormalizedView: earlier,
		nodes:          flat.Nodes,
		connections:    map[string]types.OrderedMap[int]{},
		modules:        flat.Modules,
		pruned:         flat.Pruned,
		contributed:    map[string][]string{},
		references:     references,
	}

	// A connection an attachment contributed is excluded, the same exclusion the
	// fact's own declaration states: those socket names are declared on the
	// Feature the attachment names, a separate socket set on a separate entity,
	// so counting one would report a component's own socket as populated by
	// something that cannot populate it.
	for _, e := range edges {
		if e.OriginFeature != "" {
			continue
		}
		counted := v.connections[e.Source]
		count, _ := counted.Get(e.Socket)
		counted.Set(e.Socket, count+1)
		v.connections[e.Source] = counted
	}

	// A node is attributed to the module that contributed it AND to every module
	// that module is nested inside, because a placement covers everything it
	// contains: routing an outer module into a boundary places the components of
	// the modules nested within it too. Walking the node set in its own order
	// keeps each module's list in the order the composition contributed them.
	for nodeID, node := range flat.Nodes.All() {
		if node.SourceAggregate == "" {
			continue
		}
		for _, module := range flat.Modules {
			if types.IsUnderModulePrefix(node.SourceAggregate, module.Path) {
				v.contributed[module.Path] = append(v.contributed[module.Path], nodeID)
			}
		}
	}
	return v
}

// FlattenedNodeComponentType returns the component type a node in the flattened
// set carries, and whether a node of that name is in the set at all.
func (v *topologyView) FlattenedNodeComponentType(node string) (string, bool) {
	n, ok := v.nodes.Get(node)
	if !ok || n == nil {
		return "", false
	}
	return n.ComponentType, true
}

// ComponentDeactivated returns whether the composition removed a component
// because its activation toggle read false.
func (v *topologyView) ComponentDeactivated(node string) bool {
	removed, recorded := v.pruned.Get(node)
	return recorded && removed
}

// ResolvedParameter returns the value one parameter resolved to for a node, and
// whether that node's context resolved the parameter at all.
func (v *topologyView) ResolvedParameter(node string, name string) (any, bool) {
	n, ok := v.nodes.Get(node)
	if !ok || n == nil {
		return nil, false
	}
	value, resolved := n.ResolvedParameters[name]
	return value, resolved
}

// ResolvedUsesSocketConnections returns the connections a node ends up with once
// every origin that draws one has contributed, one entry per socket carrying at
// least one, in the order they were drawn.
func (v *topologyView) ResolvedUsesSocketConnections(node string) types.OrderedMap[int] {
	return v.connections[node]
}

// NodeBoundaryPlacement returns the boundary a node was placed in, and whether
// anything placed it.
//
// The two axes a deployment places a component on are already reconciled in what
// this reads: the build writes a node's placement from the per-component map,
// which the deployment's own two axes were folded into, and from the placement a
// routed module confers on every component it contains. So a component named by
// its dotted id and a component reached by routing its module are both placed
// here, and neither needs an axis-specific reading.
func (v *topologyView) NodeBoundaryPlacement(node string) (string, bool) {
	n, ok := v.nodes.Get(node)
	if !ok || n == nil {
		return "", false
	}
	if n.BoundaryPath == "" {
		return "", false
	}
	return n.BoundaryPath, true
}

// ModuleContributedNodes returns the nodes a sub-module contributed to the
// flattened set, in the order the composition contributed them, including the
// nodes contributed by the modules nested inside it.
func (v *topologyView) ModuleContributedNodes(module string) []string {
	return v.contributed[module]
}

// BoundaryDeclaredComponentReferences returns the components the deployment's
// boundaries name, by the id each entry addresses in the flattened composition,
// in the order the deployment declares them.
func (v *topologyView) BoundaryDeclaredComponentReferences() []string {
	if len(v.references) == 0 {
		return nil
	}
	out := make([]string, 0, len(v.references))
	for _, reference := range v.references {
		out = append(out, reference.ID)
	}
	return out
}

// BoundaryComponentReferences returns every component entry the deployment's
// boundaries declare, in the order the deployment declares them: a boundary's own
// entries before the sub-modules it routes, and an enclosing boundary before the
// boundaries nested inside it.
//
// It hands back the same entries the fact beside it names, projected from the one
// fold this value took rather than derived a second time, so the entry a rule is
// invoked for and the set that fact reports cannot come to disagree.
func (v *topologyView) BoundaryComponentReferences() []constraint.BoundaryComponentReferenceInstance {
	return v.references
}

// boundaryComponentReferences folds the component entries a deployment's
// boundaries declare into the shape a traversal over them enumerates.
//
// It walks the NORMALIZED deployment, so a document that declares its components
// at the top level rather than under named boundaries is covered by the same
// walk: normalization gives such a document a boundary of its own holding exactly
// those entries, and that boundary contributes no scope level, so its entries are
// named at the top-level shape their author wrote.
//
// The two axes a boundary names components on are reconciled by the one
// traversal that reconciles them, which is what supplies both the id an entry
// addresses and the keys the author typed to write it. Reading either map
// directly would be a second reconciliation, free to disagree with the one the
// placement itself was derived from — and a reader that took a routing entry's
// local name for an id would report every module-routed deployment as addressing
// components that do not exist.
//
// The order is the deployment's own at every level, so a rule invoked once per
// entry reports in the order an author reads the document.
func boundaryComponentReferences(rel *types.Release) []constraint.BoundaryComponentReferenceInstance {
	normalized := types.NormalizeRelease(rel).Release()
	if normalized == nil {
		return nil
	}
	var out []constraint.BoundaryComponentReferenceInstance
	var walk func(scopePath string, b types.Boundary)
	walk = func(scopePath string, b types.Boundary) {
		for _, decl := range types.BoundaryComponentDeclarations(b) {
			out = append(out, referenceInstance(scopePath, decl))
		}
		for name, child := range b.Boundaries.All() {
			walk(constraint.JoinBoundaryScope(scopePath, name, child.Synthesized), child)
		}
	}
	for name, b := range normalized.Spec.Boundaries.All() {
		walk(constraint.JoinBoundaryScope("", name, b.Synthesized), b)
	}
	return out
}

// referenceInstance presents one declaration as the site a locator is built
// from.
//
// The ordinary shape is the second one, and it covers both axes a boundary
// declares on without telling them apart: the reconciliation reports the keys the
// author typed relative to the boundary, so a boundary's own entry contributes
// its component container and its key while a routing entry contributes the
// routing container, the module, the component container and the local name. The
// site names the boundary and those keys follow it, which is one rule rather than
// a branch per axis — and it is what keeps this from re-deriving which axis an
// entry came from, a question the reconciliation has already answered and the
// shape of an id cannot.
//
// The first shape exists for the one boundary an author did not write. A
// deployment that names no boundaries has its entries folded into a boundary
// normalization synthesized, which spells no level, so the keys relative to it
// have nothing to be relative to and naming them alone would produce a path the
// document does not contain. Those entries are named at the top-level site the
// author did write instead. Such a boundary holds no routing entries at all — it
// is built from the deployment's own component surface — so its declarations are
// the direct ones and the component travels on the site.
func referenceInstance(
	scopePath string,
	decl types.BoundaryComponentDeclaration,
) constraint.BoundaryComponentReferenceInstance {
	if scopePath == "" {
		return constraint.BoundaryComponentReferenceInstance{
			ID:   decl.ID,
			Site: constraint.AuthoredSite{Component: decl.ID},
		}
	}
	return constraint.BoundaryComponentReferenceInstance{
		ID:         decl.ID,
		Site:       constraint.AuthoredSite{ScopePath: scopePath},
		RoutedKeys: decl.Locator,
	}
}

// Nodes returns every component instance the flattened set holds, in the order
// the composition produced them, each carrying the provenance a locator is built
// from.
//
// The order is the flattened set's own rather than a sort, so a rule invoked
// once per node reports in the order the authors wrote at every level, and the
// nodes a module contributed appear where the composition placed them.
//
// The identifier is the one every keyed fact at this stage takes, and the
// authored name beside it is the name the author typed — a component a module
// contributed carries the module's prefix in the first and not in the second, so
// the two cannot be collapsed. The authored name is derived by the framework
// rather than here, so a component's locator built at this stage and the same
// component's locator built at a later one cannot disagree.
func (v *topologyView) Nodes() []constraint.NodeInstance {
	if v.nodes.Len() == 0 {
		return nil
	}
	out := make([]constraint.NodeInstance, 0, v.nodes.Len())
	for id, node := range v.nodes.All() {
		if node == nil {
			continue
		}
		out = append(out, constraint.NodeInstance{
			ID:                   id,
			SourceFile:           node.SourceFile,
			AuthoredName:         constraint.AuthoredComponentName(id),
			SubsystemPath:        node.SubsystemPath,
			SynthesizedSubsystem: node.SynthesizedSubsystem,
		})
	}
	return out
}

// NodeSockets returns the dependency sockets a node's component type declares,
// in that type's own author order.
//
// The axis is the DECLARED socket set and not the set the node wrote. A rule
// invoked once per socket is asked about sockets the author left empty as much
// as about the ones they filled, so an axis built from what was written would
// never present the case such a rule exists for.
//
// The set is read through the earlier stage's presentation, which already
// answers what a component type declares, so the socket a rule is invoked for
// and the requirement it then reads come from one reading of the catalog. A node
// whose type the catalog holds no entry for contributes no socket, and what that
// absence means is a separate rule's question.
func (v *topologyView) NodeSockets(node string) []string {
	componentType, present := v.FlattenedNodeComponentType(node)
	if !present {
		return nil
	}
	return v.DeclaredSocketNames(componentType)
}

// Modules returns every sub-module the composition brought in, in author order
// with an importing module before the modules nested inside it, each carrying
// the document that declared it.
//
// The order is flattening's own record rather than a sort, so it is the order the
// authors wrote at every level and a rule raised per module reports in that
// order.
func (v *topologyView) Modules() []constraint.AuthoredCoordinate {
	if len(v.modules) == 0 {
		return nil
	}
	out := make([]constraint.AuthoredCoordinate, 0, len(v.modules))
	for _, m := range v.modules {
		out = append(out, constraint.AuthoredCoordinate{Value: m.Path, SourceFile: m.DeclaredIn})
	}
	return out
}

// The compiler holds this type to the contract rather than a comment claiming
// it: a signature drifting from the declared facts stops compiling here, and the
// axis the module rule is keyed by is held the same way.
var (
	_ constraint.TopologyView                         = (*topologyView)(nil)
	_ constraint.ModuleEnumerator                     = (*topologyView)(nil)
	_ constraint.NodeEnumerator                       = (*topologyView)(nil)
	_ constraint.NodeSocketEnumerator                 = (*topologyView)(nil)
	_ constraint.BoundaryComponentReferenceEnumerator = (*topologyView)(nil)
	_ StageAxes                                       = (*topologyView)(nil)
)

// newStageView builds the presentation of the composition this build produced,
// with the earlier stage's facts answered by the presentation that stage's own
// documents produce.
//
// It returns nil when the build holds no resolve result, because the earlier
// stage's presentation is built from the documents that result carries and a
// presentation without them would answer every earlier fact from nothing.
func newStageView(cfg BuildConfig, flat *flattenResult, edges []NormalizedEdge) *topologyView {
	rr := cfg.ResolveResult
	if rr == nil || rr.Aggregate == nil {
		return nil
	}
	earlier := resolver.NewNormalizedView(
		types.Normalize(rr.Aggregate), rr.Release, cfg.CatalogLookup, rr.Parameters)
	return newTopologyView(flat, edges, earlier, boundaryComponentReferences(rr.Release))
}
