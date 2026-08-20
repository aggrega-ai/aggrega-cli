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

	// attachments is every attachment each node carries, by that node's
	// identity, as flattening collected them.
	attachments map[string][]FeatureAttachment

	// connections is each node's socket connection counts, folded once when this
	// value was built so a reader does not walk the edge set per node.
	connections map[string]types.OrderedMap[int]

	// modules is every sub-module the composition brought in, in the order
	// flattening recorded them.
	modules []ModuleDeclaration

	// contributed is the nodes each module contributed, keyed by the module's
	// dotted prefix and including the nodes its nested modules contributed.
	contributed map[string][]string
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
) *topologyView {
	v := &topologyView{
		NormalizedView: earlier,
		nodes:          flat.Nodes,
		attachments:    flat.FeatureAttachments,
		connections:    map[string]types.OrderedMap[int]{},
		modules:        flat.Modules,
		contributed:    map[string][]string{},
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

// FlattenedAttachmentType returns the Feature type one attachment on a node
// names, and whether the node carries an attachment under that name.
func (v *topologyView) FlattenedAttachmentType(node string, attachment string) (string, bool) {
	for _, a := range v.attachments[node] {
		if a.InstanceName == attachment {
			return a.Feature.Type, true
		}
	}
	return "", false
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
	_ constraint.TopologyView         = (*topologyView)(nil)
	_ constraint.ModuleEnumerator     = (*topologyView)(nil)
	_ constraint.NodeEnumerator       = (*topologyView)(nil)
	_ constraint.NodeSocketEnumerator = (*topologyView)(nil)
	_ StageAxes                       = (*topologyView)(nil)
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
	return newTopologyView(flat, edges, earlier)
}
