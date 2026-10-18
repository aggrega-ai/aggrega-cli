// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"slices"
	"strings"

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
// them. An earlier-stage fact over the Aggregate's own instance list answers
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

	// edges is every connection the composition drew, in the order it drew them.
	// The facts keyed by a connection are keyed rather than enumerated, so this
	// is walked per read instead of folded: a connection set is small beside the
	// node set, and folding it would hold a second index of what the
	// composition already orders.
	edges []NormalizedEdge

	// modules is every sub-module the composition brought in, in the order
	// flattening recorded them.
	modules []ModuleDeclaration

	// pruned is every node a false activation toggle removed from the
	// composition. Only ever stores true, so presence is membership.
	pruned types.OrderedMap[bool]

	// declaringSubsystems is the subsystems that declared a component under each
	// identity, in the order the composition met them. It holds every
	// declaration rather than the surviving one, which is what lets a rule ask
	// whether an identity was declared more than once.
	declaringSubsystems types.OrderedMap[[]string]

	// contributed is the nodes each module contributed, keyed by the module's
	// dotted prefix and including the nodes its nested modules contributed.
	contributed map[string][]string

	// references is every component entry the Release's boundaries declare,
	// on both of the axes a boundary names components on, in the order the
	// Release declares them.
	//
	// It is what the Release asked for and is deliberately not reconciled
	// against the node set above: an entry addressing an id nothing answers to
	// is exactly the entry a rule over this axis exists to be asked about, so
	// folding the two together here would remove the subject before any rule saw
	// it.
	references []constraint.BoundaryComponentReferenceInstance

	// edgeConfigurations is every entry the Release's per-edge `uses` surface
	// writes, in the order the document declares them.
	//
	// It is what the Release wrote and is deliberately not reconciled against the
	// connection set above: an entry whose connection nothing drew is exactly the
	// entry a rule over this axis exists to be asked about, and the connection set
	// holds one instance per connection drawn, so folding the two together here
	// would remove the subject before any rule saw it.
	edgeConfigurations []constraint.ReleaseEdgeConfigurationInstance

	// boundaryScopes is every boundary the Release declares, an enclosing scope
	// before the scopes nested inside it, each with the attachments it declares on
	// itself in author order.
	//
	// It is folded once, when this value is built, because the presentation holds
	// no authored document: the axis over boundary-wide attachments and the fact
	// naming what each reaches are both answered from this one reading, so the
	// attachment a rule is invoked for is one the fact can answer about.
	boundaryScopes []releaseBoundaryScope

	// recipeResolutions is what each node's Recipe resolution came to, joined
	// once when this value was built.
	//
	// It is folded here rather than resolved per read because four facts project
	// it and a rule asked about a node reads all four: resolving per read would
	// perform the same join four times per node, and the join scans the recipe
	// index for a node nothing maps. Folding also keeps the four answers about
	// one node consistent with each other, which per-read resolution could not
	// promise if the index moved between them.
	//
	// A node absent from the map resolved to nothing, which is the same answer a
	// node the mapping does not reach gives. The two are not separated here
	// because nothing separates them downstream either: a build whose catalog
	// resolves no recipe has resolved no instance to one, exactly as the
	// catalog-presence facts on the stage this value contains already report.
	recipeResolutions map[string]resolver.NodeRecipeResolution
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
	edgeConfigurations []constraint.ReleaseEdgeConfigurationInstance,
	boundaryScopes []releaseBoundaryScope,
	resolveRecipe nodeRecipeResolver,
) *topologyView {
	v := &topologyView{
		NormalizedView:      earlier,
		nodes:               flat.Nodes,
		connections:         map[string]types.OrderedMap[int]{},
		edges:               edges,
		modules:             flat.Modules,
		pruned:              flat.Pruned,
		declaringSubsystems: flat.DeclaringSubsystems,
		contributed:         map[string][]string{},
		references:          references,
		edgeConfigurations:  edgeConfigurations,
		boundaryScopes:      boundaryScopes,
		recipeResolutions:   map[string]resolver.NodeRecipeResolution{},
	}

	// Each node's Recipe resolution, joined once, over the flattened set — so a
	// component a sub-aggregate contributed is resolved by the same join as one
	// the Aggregate declared, under the qualified identity the composition gave
	// it. That identity is what the mapping's name axis is matched against, and
	// it exists only here.
	if resolveRecipe != nil {
		for nodeID, node := range flat.Nodes.All() {
			if node == nil {
				continue
			}
			v.recipeResolutions[nodeID] = resolveRecipe(nodeID, node.ComponentType)
		}
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

// nodeRecipeResolver joins the Manifest's mapping table and the catalog's recipe
// index into the answer about one component instance, given the identity the
// composition gave it and the type it carries.
//
// It is a callback rather than a direct reach for the package that owns the
// mapping mechanic, for the reason the sub-aggregate loader beside it is one:
// that package reads this one, so reaching it from here would close a cycle.
// The seam also keeps a composition built from in-memory fixtures resolvable
// without a filesystem catalog behind it.
type nodeRecipeResolver func(instanceName, componentType string) resolver.NodeRecipeResolution

// FlattenedNodeComponentType returns the component type a node in the flattened
// set carries, and whether a node of that name is in the set at all.
func (v *topologyView) FlattenedNodeComponentType(node string) (string, bool) {
	n, ok := v.nodes.Get(node)
	if !ok || n == nil {
		return "", false
	}
	return n.ComponentType, true
}

// NodeMappedRecipeReference returns the Recipe reference the Manifest's mapping
// names for a node, and whether the mapping reached that node at all.
func (v *topologyView) NodeMappedRecipeReference(node string) (string, bool) {
	resolution := v.recipeResolutions[node]
	return resolution.Reference, resolution.Mapped
}

// NodeRecipeMappingAmbiguousKeys returns the version-distinct mapping keys a
// node's component type matched when the lookup could not choose between them,
// empty when it settled.
func (v *topologyView) NodeRecipeMappingAmbiguousKeys(node string) []string {
	return v.recipeResolutions[node].AmbiguousKeys
}

// NodeMappedRecipePresent returns whether the catalog holds a Recipe at the
// reference the Manifest's mapping names for a node.
func (v *topologyView) NodeMappedRecipePresent(node string) bool {
	return v.recipeResolutions[node].Present
}

// NodeRecipePinCandidates returns the catalog Recipes offered as pin candidates
// for a node the Manifest's mapping did not reach, sorted, empty for a node it
// did.
func (v *topologyView) NodeRecipePinCandidates(node string) []string {
	return v.recipeResolutions[node].PinCandidates
}

// ComponentDeactivated returns whether the composition removed a component
// because its activation toggle read false.
func (v *topologyView) ComponentDeactivated(node string) bool {
	removed, recorded := v.pruned.Get(node)
	return recorded && removed
}

// NodeDeclaringSubsystems returns the subsystems that declared a component under
// the identity a node carries, in the order the composition met each
// declaration.
func (v *topologyView) NodeDeclaringSubsystems(node string) []string {
	declaring, _ := v.declaringSubsystems.Get(node)
	return declaring
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

// FlattenedConnectionAttachmentNames returns the attachments the connections of a
// node in the flattened set carry, keyed by socket and then by alias, each
// listing the names its attachments are written under, in author order.
//
// It reads the node's own authored block, which flattening carries on the node
// for a component wherever its document sits, so a component a sub-module
// contributes answers exactly as one the Aggregate declares. A connection
// carrying no attachment contributes nothing.
func (v *topologyView) FlattenedConnectionAttachmentNames(node string) types.OrderedMap[types.OrderedMap[[]string]] {
	out := types.OrderedMap[types.OrderedMap[[]string]]{}
	n, ok := v.nodes.Get(node)
	if !ok || n == nil {
		return out
	}
	for socket, sv := range n.Uses.All() {
		aliases := types.OrderedMap[[]string]{}
		for alias, entry := range sv.Verbose.All() {
			if entry.Features.Len() == 0 {
				continue
			}
			aliases.Set(alias, entry.Features.Keys())
		}
		if aliases.Len() > 0 {
			out.Set(socket, aliases)
		}
	}
	return out
}

// FlattenedConnectionAttachmentType returns the Feature type one attachment the
// developer wrote on one connection of a node in the flattened set names, and
// whether that connection carries an attachment under the name at all.
//
// It reads the same authored block [topologyView.FlattenedConnectionAttachmentNames]
// reads, so a name that fact lists is a name this answers for.
func (v *topologyView) FlattenedConnectionAttachmentType(node string, socket string, alias string, attachment string) (string, bool) {
	n, ok := v.nodes.Get(node)
	if !ok || n == nil {
		return "", false
	}
	sv, held := n.Uses.Get(socket)
	if !held {
		return "", false
	}
	entry, held := sv.Verbose.Get(alias)
	if !held {
		return "", false
	}
	written, held := entry.Features.Get(attachment)
	if !held {
		return "", false
	}
	return written.Type, true
}

// PerEdgeAuthoredTarget returns the target one connection names, as the document
// that drew it wrote the name, empty when the composition drew no connection
// there.
func (v *topologyView) PerEdgeAuthoredTarget(node string, socket string, alias string) string {
	e, drawn := v.connectionAt(node, socket, alias)
	if !drawn {
		return ""
	}
	return e.AuthoredTarget
}

// PerEdgeConnectionDrawn returns whether the composition drew a connection at one
// coordinate, whichever surface drew it.
func (v *topologyView) PerEdgeConnectionDrawn(node string, socket string, alias string) bool {
	_, drawn := v.connectionAt(node, socket, alias)
	return drawn
}

// connectionAt returns the connection the composition drew at one coordinate, and
// whether it drew one at all.
//
// It is the one lookup both connection-keyed facts read, so membership and the
// value a member carries are two readings of one search rather than two searches
// free to disagree about which connection they found.
func (v *topologyView) connectionAt(node, socket, alias string) (NormalizedEdge, bool) {
	for i := range v.edges {
		e := v.edges[i]
		if e.Source == node && e.Socket == socket && e.Alias == alias {
			return e, true
		}
	}
	return NormalizedEdge{}, false
}

// NodeBoundaryPlacement returns the boundary a node was placed in, and whether
// anything placed it.
//
// The two axes a Release places a component on are already reconciled in what
// this reads: the build writes a node's placement from the per-component map,
// which the Release's own two axes were folded into, and from the placement a
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

// BoundaryDeclaredComponentReferences returns the components the Release's
// boundaries name, by the id each entry addresses in the flattened composition,
// in the order the Release declares them.
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

// BoundaryComponentReferences returns every component entry the Release's
// boundaries declare, in the order the Release declares them: a boundary's own
// entries before the sub-modules it routes, and an enclosing boundary before the
// boundaries nested inside it.
//
// It hands back the same entries the fact beside it names, projected from the one
// fold this value took rather than derived a second time, so the entry a rule is
// invoked for and the set that fact reports cannot come to disagree.
func (v *topologyView) BoundaryComponentReferences() []constraint.BoundaryComponentReferenceInstance {
	return v.references
}

// ReleaseAuthoredEdgeConfigurations returns every entry the Release's per-edge
// `uses` surface writes, in the order the document declares them.
//
// It projects the coordinates off the one walk this value took rather than
// walking the document a second time, so the entry a rule is invoked for and the
// set this fact reports cannot come to describe an entry differently.
func (v *topologyView) ReleaseAuthoredEdgeConfigurations() []constraint.ReleaseEdgeConfiguration {
	if len(v.edgeConfigurations) == 0 {
		return nil
	}
	out := make([]constraint.ReleaseEdgeConfiguration, 0, len(v.edgeConfigurations))
	for _, entry := range v.edgeConfigurations {
		out = append(out, entry.Configuration)
	}
	return out
}

// ReleaseEdgeConfigurations returns every entry the Release's per-edge `uses`
// surface writes, each carrying the site it was written at, in the order the
// document declares them.
func (v *topologyView) ReleaseEdgeConfigurations() []constraint.ReleaseEdgeConfigurationInstance {
	return v.edgeConfigurations
}

// ReleaseConnectionAttachmentSite returns the site naming one attachment the
// Release wrote on one of a component's connections, and whether the Release
// wrote an entry for that connection at all.
//
// It reads the entry through the same fold the traversal over the Release's
// per-edge surface is built from, so a finding about an attachment the entry
// carries names the very entry a rule over that entry would name — the boundary
// it sits in and the keys the operator typed to reach the component — and never
// a path composed from the name the attachment carries on the consuming
// component. The connection is matched by the socket and the alias it was written
// under, which the caller holds as data.
func ReleaseConnectionAttachmentSite(
	rel *types.Release,
	sourceFile, node, socket, alias, attachment string,
) (site constraint.AttachmentSite, found bool) {
	for _, entry := range resolver.ReleaseEdgeConfigurations(rel, sourceFile) {
		c := entry.Configuration
		if c.Node == node && c.Socket == socket && c.Alias == alias {
			return constraint.ConnectionAttachmentSiteInRelease(entry, attachment), true
		}
	}
	return constraint.AttachmentSite{}, false
}

// ReleaseEntity names the Release document a finding about something the
// Release wrote is attributed to, in the spelling every finding this package
// raises against the Release uses — so a finding a later stage raises about an
// entry the Release wrote names that document exactly as this stage's findings
// do, and is filed under the same author.
func ReleaseEntity(rel *types.Release) string {
	return releaseEntityName(rel)
}

// releaseBoundaryScope is one boundary a Release declares: the coordinate an axis
// over it names, and the attachments it declares on itself in author order.
type releaseBoundaryScope struct {
	coordinate  constraint.AuthoredCoordinate
	attachments []string
}

// releaseBoundaryScopes folds the boundaries a Release declares, with the
// attachments each declares on itself, into the shape the boundary axes
// enumerate.
//
// It reads them through the one walk every stage enumerates a Release's
// boundaries by, so a coordinate named here is spelled exactly as the stages
// either side of this one spell it. A boundary normalization synthesized is not
// a scope the operator declared and contributes none, and it carries no
// attachments of its own to contribute: the Release's top level declares
// components only.
func releaseBoundaryScopes(rel *types.Release, sourceFile string) []releaseBoundaryScope {
	var out []releaseBoundaryScope
	resolver.WalkReleaseBoundaries(rel, func(path string, b types.Boundary) {
		out = append(out, releaseBoundaryScope{
			coordinate:  constraint.AuthoredCoordinate{Value: path, SourceFile: sourceFile},
			attachments: b.Features.Keys(),
		})
	})
	return out
}

// Boundaries returns every boundary the Release declares, at every depth, in
// author order with an enclosing scope before the scopes nested inside it.
func (v *topologyView) Boundaries() []constraint.AuthoredCoordinate {
	if len(v.boundaryScopes) == 0 {
		return nil
	}
	out := make([]constraint.AuthoredCoordinate, 0, len(v.boundaryScopes))
	for _, scope := range v.boundaryScopes {
		out = append(out, scope.coordinate)
	}
	return out
}

// BoundaryAttachments returns the attachments one boundary declares on itself,
// in the order the Release declares them.
func (v *topologyView) BoundaryAttachments(boundary string) []string {
	for _, scope := range v.boundaryScopes {
		if scope.coordinate.Value == boundary {
			return scope.attachments
		}
	}
	return nil
}

// BoundaryAttachmentReachedNodes returns the nodes one attachment a boundary
// declares on itself reaches: every node placed in that boundary or in a boundary
// nested inside it, in the order the composition produced them, and none when
// the boundary declares no attachment under that name.
//
// The reach is read off the placement each node carries, which is the same
// placement the build attaches a boundary-wide attachment by, so a rule reading
// this and the attachment the build produces cannot disagree about which
// components one boundary's attachment lands on.
func (v *topologyView) BoundaryAttachmentReachedNodes(boundary string, attachment string) []string {
	if boundary == "" || !slices.Contains(v.BoundaryAttachments(boundary), attachment) {
		return nil
	}
	// The prefix every boundary path nested inside this one begins with: the
	// boundary joined to a level not yet named, by the one join the boundary axis
	// composes its paths with.
	nested := constraint.JoinBoundaryScope(boundary, "", false)
	var out []string
	for id, node := range v.nodes.All() {
		if node == nil {
			continue
		}
		if node.BoundaryPath == boundary || strings.HasPrefix(node.BoundaryPath, nested) {
			out = append(out, id)
		}
	}
	return out
}

// boundaryComponentReferences folds the component entries a Release's
// boundaries declare into the shape a traversal over them enumerates.
//
// It walks the NORMALIZED Release, so a document that declares its components
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
// local name for an id would report every module-routed Release as addressing
// components that do not exist.
//
// The order is the Release's own at every level, so a rule invoked once per
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
// Release that names no boundaries has its entries folded into a boundary
// normalization synthesized, which spells no level, so the keys relative to it
// have nothing to be relative to and naming them alone would produce a path the
// document does not contain. Those entries are named at the top-level site the
// author did write instead. Such a boundary holds no routing entries at all — it
// is built from the Release's own component surface — so its declarations are
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

// Edges returns every connection a node's own dependency block wrote, for every
// node the flattened set holds: in node order, then in each block's socket order
// and, within a socket, in the order its connections were written.
//
// The axis is the developers' own blocks and deliberately nothing else. The
// connection set the composition drew also holds connections an attachment or
// the Release drew, and a rule over what a developer wrote on a connection has to
// be asked once per connection a developer wrote — including one the composition
// drew nothing for, which is the gap in the connection set. Reading the blocks
// the flattened nodes carry is what makes a component a sub-module contributes
// enumerate exactly as one the Aggregate declares, positioned in the document it
// was written in.
//
// Each connection's alias is read through the one projection of a socket's
// authored value onto its connections, so a connection the short form listed is
// named by its target and marked as an alias no key spells — the locator then
// stops at the socket, which is what the author wrote.
func (v *topologyView) Edges() []constraint.EdgeInstance {
	var out []constraint.EdgeInstance
	for _, node := range v.Nodes() {
		n, ok := v.nodes.Get(node.ID)
		if !ok || n == nil {
			continue
		}
		for socket, sv := range n.Uses.All() {
			shortForm := len(sv.Compact) > 0
			for _, ev := range resolver.EdgeValuesOf(sv) {
				out = append(out, constraint.EdgeInstance{
					Node:             node,
					Socket:           socket,
					Alias:            ev.Alias,
					SynthesizedAlias: shortForm,
				})
			}
		}
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
	_ constraint.EdgeEnumerator                       = (*topologyView)(nil)
	_ constraint.BoundaryComponentReferenceEnumerator = (*topologyView)(nil)
	_ constraint.ReleaseEdgeConfigurationEnumerator   = (*topologyView)(nil)
	_ constraint.BoundaryEnumerator                   = (*topologyView)(nil)
	_ constraint.BoundaryAttachmentEnumerator         = (*topologyView)(nil)
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
	return newTopologyView(
		flat, edges, earlier,
		boundaryComponentReferences(rr.Release),
		resolver.ReleaseEdgeConfigurations(rr.Release, rr.ReleasePath),
		releaseBoundaryScopes(rr.Release, rr.ReleasePath),
		nodeRecipeResolverOver(cfg.CatalogLookup, rr.Manifest),
	)
}

// nodeRecipeResolverOver binds the Manifest this build is compiling against to
// the catalog's own joint resolution, so the composition resolves each node
// through the same lookup the render resolves through.
//
// It returns nil when the catalog resolves no recipes, which is what a build
// running without a recipe index has: no instance resolved to a Recipe. The
// facts over the result then answer as they do for a node the mapping does not
// reach, which is the reading the catalog-presence facts on the stage this one
// contains already take.
//
// The Manifest is bound here rather than carried into the view because the view
// projects facts and holds no authored document: binding it at the seam is what
// keeps the presentation free of a document it does not present.
func nodeRecipeResolverOver(
	catalog resolver.CatalogLookup,
	manifest *types.Manifest,
) nodeRecipeResolver {
	if catalog.NodeRecipeResolution == nil {
		return nil
	}
	return func(instanceName, componentType string) resolver.NodeRecipeResolution {
		return catalog.NodeRecipeResolution(manifest, instanceName, componentType)
	}
}
