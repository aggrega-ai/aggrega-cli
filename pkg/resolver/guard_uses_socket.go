// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// releaseEdgeEntry is one operator-authored per-edge connection collected from
// a Release: the consuming node ID, the socket and alias the entry sits under,
// the boundary path it was authored in (for diagnostics), and the entry itself.
// synthesized is true when the containing boundary is the compiler-synthesized
// default — the component was authored at the top-level `.spec.components`
// surface, so [releaseComponentFieldPath] renders its diagnostics at that root
// shape rather than under the generated `default` boundary.
type releaseEdgeEntry struct {
	nodeID       string
	socket       string
	alias        string
	boundaryPath string
	locator      []string
	synthesized  bool
	entry        types.ReleaseUsesEntry
}

// releaseComponentFieldPath renders the authoring field locator for a Release
// boundary component, the Release-side mirror of [componentFieldPath]. A
// synthesized-default component renders at the root shape
// `.spec.components["node"]` — where the author typed it — while a component in
// a declared boundary renders the qualified
// `.spec.boundaries["path"].components["node"]`. The key is the
// synthesized-provenance flag, not the path string: a boundary a user declares
// with the name "default" still renders the qualified path.
//
// locator is the key path within the boundary, from
// [types.BoundaryComponentLocator]. A component the boundary routes in from a
// sub-module locates through the routing entry and under its local name —
// `.aggregates["data"].components["consumer"]` — because that is the key the
// author wrote; naming it by its qualified id would send them looking for a key
// that is not in the document, and one the Release schema would not accept there.
func releaseComponentFieldPath(boundaryPath string, locator []string, synthesized bool) string {
	if len(locator) == 0 {
		return fmt.Sprintf(".spec.boundaries[%q]", boundaryPath)
	}
	if synthesized {
		return fmt.Sprintf(".spec.components[%q]", locator[len(locator)-1])
	}
	var b strings.Builder
	fmt.Fprintf(&b, ".spec.boundaries[%q]", boundaryPath)
	for i := 0; i+1 < len(locator); i += 2 {
		fmt.Fprintf(&b, ".%s[%q]", locator[i], locator[i+1])
	}
	return b.String()
}

// ReleaseEdgeEntry is one operator-authored per-edge connection collected from a
// Release, in the shape a consumer outside this package reads it: the consuming
// node ID, the socket and alias the entry sits under, and the entry itself.
//
// It exists because the same Release edge surface is validated at two stages —
// once for every consuming node whose component type is knowable before
// flattening, and once after flattening for the nodes that only exist then. Both
// read the entries through [ReleaseEdgeEntries], so neither can disagree with the
// other about which entries a Release declares or in what order.
type ReleaseEdgeEntry struct {
	NodeID string
	Socket string
	Alias  string
	Entry  types.ReleaseUsesEntry
}

// ReleaseEdgeEntries walks the Release boundary tree (outer to inner, author
// order) and flattens every per-edge `uses` entry on every boundary component
// into a deterministic slice.
//
// The boundary path each entry was authored in is deliberately not carried: a
// consumer outside this package addresses the consuming node by its flattened ID,
// and a boundary-qualified authoring locator is only meaningful where the
// pre-flatten document shape is still in view.
func ReleaseEdgeEntries(release *types.Release) []ReleaseEdgeEntry {
	refs := collectReleaseEdgeEntries(release)
	if len(refs) == 0 {
		return nil
	}
	out := make([]ReleaseEdgeEntry, 0, len(refs))
	for _, r := range refs {
		out = append(out, ReleaseEdgeEntry{
			NodeID: r.nodeID,
			Socket: r.socket,
			Alias:  r.alias,
			Entry:  r.entry,
		})
	}
	return out
}

// ReleaseEntryCreatesEdge reports whether a Release per-edge `uses` entry draws
// a new edge, rather than configuring one that already exists.
//
// An entry naming a target creates an edge into a declared-but-unbound socket,
// pointing at an existing component. A targetless entry carries per-edge `infra`
// only and configures an edge some other authoring document already drew, so it
// draws nothing itself.
//
// The rule has one home because two stages ask it from opposite sides and must
// not disagree: the graph-build pass asks it to decide whether to draw the edge,
// and the required-socket rule asks it to decide whether a socket counts as
// bound. Were the two to answer differently, a socket the second omitted would
// be reported as missing its required binding while the graph that gets rendered
// wires it — a document rejected for a connection the build itself makes.
func ReleaseEntryCreatesEdge(entry types.ReleaseUsesEntry) bool {
	return entry.Target != ""
}

// collectReleaseEdgeEntries walks the Release boundary tree (outer to inner,
// author order) and flattens every per-edge `uses` entry on every boundary
// component into a deterministic slice. Shared by the Step-1 guards that need
// the Release edge surface — undeclared-socket validation and per-edge infra
// conformance — so both agree on which entries exist and in what order.
// [ReleaseEdgeEntries] projects the same walk for consumers outside this package.
func collectReleaseEdgeEntries(release *types.Release) []releaseEdgeEntry {
	if release == nil {
		return nil
	}
	var out []releaseEdgeEntry
	var walk func(path string, b types.Boundary)
	walk = func(path string, b types.Boundary) {
		components := types.BoundaryComponents(b)
		for nodeID, bc := range components.All() {
			if bc.Uses.Len() == 0 {
				continue
			}
			locator := types.BoundaryComponentLocator(b, nodeID)
			for socket, aliases := range bc.Uses.All() {
				for alias, e := range aliases.All() {
					out = append(out, releaseEdgeEntry{
						nodeID:       nodeID,
						socket:       socket,
						alias:        alias,
						boundaryPath: path,
						locator:      locator,
						synthesized:  b.Synthesized,
						entry:        e,
					})
				}
			}
		}
		for name, nested := range b.Boundaries.All() {
			child := name
			if path != "" {
				child = path + "/" + name
			}
			walk(child, nested)
		}
	}
	for name, b := range release.Spec.Boundaries.All() {
		walk(name, b)
	}
	return out
}

// guardReleaseUsesSocket validates that every socket a Release per-edge `uses`
// entry names exists in the consuming component's catalog usesSchema, firing
// ErrUnknownUsesSocket per undeclared socket. The check sits with the resolver,
// which owns the socket machinery and the catalog, so a Release wiring an edge
// into a non-existent socket halts before topology draws it.
//
// This halt survives the relaxation on the component's own `uses` surface, and its
// survival is deliberate rather than an oversight. The Release surface may wire and
// configure an edge within a socket the architecture declares; it may not invent
// the socket. Letting it do so would let the operator surface introduce topology
// the Aggregate never declared, which is a much stronger claim than letting the
// Aggregate author name a socket for their own component. This function is not a
// second copy of [ValidateUsesAgainstSchema]'s rule — it enforces a
// same-shaped condition on a different surface with different authority, so the two
// are kept apart on purpose and must not be unified.
//
// The consuming node's component type is resolved from the Aggregate's
// resolver-visible instances, which is every component the Aggregate declares:
// those at the top level and those in a declared subsystem, at any nesting
// depth. Both are addressed by their bare instance name, which is the same
// identity a Release names them by, so a subsystem component is inspected here
// like any other. A Release edge on a node whose type is not among them — a
// sub-aggregate component, whose document loads only when the graph is flattened
// — is skipped here and validated once flattening has made it visible, so the
// skip is a division of labour rather than a hole. That coverage boundary is
// shared with the per-edge conformance gate.
func guardReleaseUsesSocket(na types.NormalizedAggregate, release *types.Release, catalog CatalogLookup) []*ResolverError {
	agg := na.Aggregate()
	if agg == nil || release == nil || catalog.Component == nil {
		return nil
	}
	entity := fmt.Sprintf("Release %s", release.Metadata.Name)
	typeOf := map[string]string{}
	for _, p := range collectComponentInstances(na) {
		typeOf[p.name] = p.inst.Type
	}

	var errs []*ResolverError
	for _, ref := range collectReleaseEdgeEntries(release) {
		compType, ok := typeOf[ref.nodeID]
		if !ok {
			continue
		}
		catComp, found := catalog.Component(compType)
		if !found {
			if verr := guardEntityViewInconsistency(entity,
				releaseComponentFieldPath(ref.boundaryPath, ref.locator, ref.synthesized), compType); verr != nil {
				errs = append(errs, verr)
			}
			continue
		}
		if _, ok := catComp.Spec.UsesSchema.Get(ref.socket); !ok {
			errs = append(errs, &ResolverError{
				Entity: entity,
				Field:  fmt.Sprintf("%s.uses[%q]", releaseComponentFieldPath(ref.boundaryPath, ref.locator, ref.synthesized), ref.socket),
				Message: fmt.Sprintf("socket %q not defined in component type %q usesSchema (valid: %v)",
					ref.socket, compType, catComp.Spec.UsesSchema.Keys()),
				Err: ErrUnknownUsesSocket(),
			})
		}
	}
	sortResolverErrors(errs)
	return errs
}

// scopeAttachment is one Feature attachment declared at a scope rather than on a
// single component instance: a subsystem's `features` block, a Release boundary's,
// or a Release boundary-component's. Field is the authoring locator of the
// attachment's own `uses` block.
type scopeAttachment struct {
	field    string
	featType string
	uses     types.OrderedMap[types.UsesSocketValue]
}

// collectAggregateScopeAttachments walks the Aggregate's subsystem tree in author
// order and yields every subsystem-level Feature attachment that declares a
// `uses` block. Nested subsystems are included, with the dotted-path locator
// convention the component-side diagnostics use.
//
// A subsystem-level attachment contributes its uses-edge from every component in
// the scope, so its socket names are as load-bearing as a per-component
// attachment's — and they name sockets on the FEATURE, which is why one validator
// serves both.
func collectAggregateScopeAttachments(na types.NormalizedAggregate) []scopeAttachment {
	agg := na.Aggregate()
	if agg == nil {
		return nil
	}
	var out []scopeAttachment
	// This walk reads no component instances, so it declares no per-instance
	// callback and the container hook carries all of it. No callback fails, so the
	// walk cannot.
	_ = types.WalkInstanceTree(agg.Spec.Subsystems, types.SubsystemTree(), "", struct{}{},
		types.InstanceTreeVisitor[types.Subsystem, types.ComponentInstance, struct{}]{
			Path: types.JoinSubsystemPath,
			Leave: func(sub types.Subsystem, _, path string, _ struct{}) {
				for name, feat := range sub.Features.All() {
					if feat.Uses.Len() == 0 {
						continue
					}
					out = append(out, scopeAttachment{
						field:    fmt.Sprintf("%s.features[%q].uses", subsystemFieldPath(path), name),
						featType: feat.Type,
						uses:     feat.Uses,
					})
				}
			},
		})
	return out
}

// subsystemFieldPath renders the authoring locator of a subsystem itself, the
// scope-level sibling of [componentFieldPath]'s component locator.
func subsystemFieldPath(subsystemPath string) string {
	return ".spec.subsystems." + strings.ReplaceAll(subsystemPath, ".", ".subsystems.")
}

// collectReleaseScopeAttachments walks the Release boundary tree in author order
// (outer to inner, own attachments before nested boundaries) and yields every
// Feature attachment that declares a `uses` block, from both Release-side scopes:
// a boundary's own `features` and a boundary component's `features`.
func collectReleaseScopeAttachments(release *types.Release) []scopeAttachment {
	if release == nil {
		return nil
	}
	var out []scopeAttachment
	// The boundary's own attachments are collected in the container hook so they
	// precede its components', which is the order the godoc above states. No
	// callback fails, so the walk cannot.
	// The accumulator carries the containing boundary so a component's authoring
	// locator can be read from the axis that declares it — the boundary's own
	// components, or a routing entry that names it under its local name.
	_ = types.WalkInstanceTree(release.Spec.Boundaries, types.BoundaryTree(), "", types.Boundary{},
		types.InstanceTreeVisitor[types.Boundary, types.BoundaryComponent, types.Boundary]{
			Path: joinBoundaryPath,
			Enter: func(b types.Boundary, _, path string, _ types.Boundary) types.Boundary {
				for name, feat := range b.Features.All() {
					if feat.Uses.Len() == 0 {
						continue
					}
					out = append(out, scopeAttachment{
						field:    fmt.Sprintf(".spec.boundaries[%q].features[%q].uses", path, name),
						featType: feat.Type,
						uses:     feat.Uses,
					})
				}
				return b
			},
			Visit: func(bc types.BoundaryComponent, nodeID, path string, synthesized bool, scope types.Boundary) error {
				for name, feat := range bc.Features.All() {
					if feat.Uses.Len() == 0 {
						continue
					}
					out = append(out, scopeAttachment{
						field: fmt.Sprintf("%s.features[%q].uses",
							releaseComponentFieldPath(path, types.BoundaryComponentLocator(scope, nodeID), synthesized), name),
						featType: feat.Type,
						uses:     feat.Uses,
					})
				}
				return nil
			},
		})
	return out
}

// guardUsesSocket walks every surface that can author a `uses` block and applies
// each surface's socket rule to it. The rule is not the same on every surface, and
// the difference is the point: an Aggregate author owns the application's topology
// and may name a socket the catalog did not declare, while an attachment may only
// name a socket the Feature it attaches declares.
//
// Component-own `uses` goes to [ValidateUsesAgainstSchema], which no longer
// requires the socket to appear in the Component's usesSchema — an author-named
// socket is accepted, and the IR edge records that the socket was not
// catalog-declared so a governance policy can refuse it. Attachment `uses` goes to
// [ValidateFeatureUsesAgainstSchema], which still halts on a socket the Feature
// does not declare.
//
// Every attachment surface these two documents declare is walked, not only the
// component instance's own: an attachment declared at a subsystem, at a Release
// boundary, or on a Release boundary component names sockets on the same Feature
// entity and is validated against the same usesSchema. Where the attachment is
// declared changes which components its edge is contributed from; it does not
// change what a valid socket name is, so one rule covers every attachment surface
// and its coverage does not depend on the author's choice of scope.
//
// One attachment surface is out of this walk's reach rather than out of the rule's:
// a component inside a sub-aggregate module, whose document is loaded only as the
// graph is flattened. Its attachments are validated once flattening has made them
// visible, through the same exported validator, so the coverage boundary is a
// division of labour rather than a hole.
func guardUsesSocket(na types.NormalizedAggregate, release *types.Release, catalog CatalogLookup) []*ResolverError {
	agg := na.Aggregate()
	if agg == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	if catalog.Feature != nil {
		for _, sa := range collectAggregateScopeAttachments(na) {
			errs = append(errs, ValidateFeatureUsesAgainstSchema(
				entity, sa.field, sa.uses, sa.featType, catalog)...)
		}
		if release != nil {
			relEntity := fmt.Sprintf("Release %s", release.Metadata.Name)
			for _, sa := range collectReleaseScopeAttachments(release) {
				errs = append(errs, ValidateFeatureUsesAgainstSchema(
					relEntity, sa.field, sa.uses, sa.featType, catalog)...)
			}
		}
	}

	// One complete walk of the subsystem tree via the shared instance walker;
	// the authoring path comes from componentFieldPath so a subsystem
	// component's diagnostics point at .spec.subsystems.<…>.components[…].
	for _, p := range collectComponentInstances(na) {
		comp := p.inst
		base := componentFieldPath(p.subsystemPath, p.name, p.synthesized)

		// Validate component uses against Component usesSchema
		if comp.Uses.Len() > 0 && catalog.Component != nil {
			errs = append(errs, ValidateUsesAgainstSchema(
				entity,
				base+".uses",
				comp.Uses,
				comp.Type,
				catalog,
			)...)
		}

		// Validate feature uses against Feature usesSchema
		if catalog.Feature != nil {
			for featName, feat := range comp.Features.All() {
				if feat.Uses.Len() > 0 {
					errs = append(errs, ValidateFeatureUsesAgainstSchema(
						entity,
						fmt.Sprintf("%s.features[%q].uses", base, featName),
						feat.Uses,
						feat.Type,
						catalog,
					)...)
				}
			}
		}
	}

	sortResolverErrors(errs)
	return errs
}

// ValidateUsesAgainstSchema inspects a component instance's uses block for the
// one surface where an Aggregate author owns the topology: the component's own
// `uses:`, whether the component sits in the Aggregate or in a flattened
// sub-aggregate.
//
// It does NOT require the socket name to appear in the catalog Component's
// usesSchema. An author may name a socket the catalog did not declare, and the
// edge is built and delivered — a catalog that declares no entry for a socket has
// not anticipated the connection, which is not the same as forbidding it. What the
// edge may carry is unchanged: every key it injects must still name a key the
// target's provides declares, and the target must still exist. The IR edge records
// which of the two cases it is (see types.IREdge.SocketDeclaration), so a
// governance policy can refuse a free-form connection outright without the
// compiler taking a position on it.
//
// Declaring the socket in the catalog remains the better path and stays what the
// standard-library catalog does: a declared socket carries a selector, which
// constrains which targets the connection may reach, and an ad-hoc socket has no
// selector to carry.
//
// The relaxation is scoped to this surface alone. A Release may still only wire
// within a socket the consuming component declares ([guardReleaseUsesSocket]), and
// a Feature attachment may still only name a socket the Feature declares
// ([ValidateFeatureUsesAgainstSchema]) — separate functions with their own inline
// checks, so those halts hold structurally rather than by care. The asymmetry is
// deliberate: the Aggregate author owns the application's topology, while the
// operator surface configures and wires within an architecture it did not author.
//
// What remains here is the catalog-view classification: a resolved bare-token
// catalog reference absent from the guard's view is a loud inconsistency, while a
// relative-path type legitimately has no catalog entity.
func ValidateUsesAgainstSchema(entity, fieldPath string, uses types.OrderedMap[types.UsesSocketValue], compType string, catalog CatalogLookup) []*ResolverError {
	if uses.Len() == 0 {
		return nil
	}
	if _, found := catalog.Component(compType); !found {
		if verr := guardEntityViewInconsistency(entity, fieldPath, compType); verr != nil {
			return []*ResolverError{verr}
		}
	}
	return nil
}

// ValidateFeatureUsesAgainstSchema validates an attachment's `uses` block against
// the attached catalog Feature's usesSchema. Two failure modes — the Feature
// declares no usesSchema, so no socket may be named; or it declares one and the
// attachment names a socket absent from it — and a silent skip on an unknown
// catalog Feature type. Used for every attachment on the one `features` surface,
// whatever the attachment produces: they all resolve through the Feature catalog
// kind.
//
// It is exported because the same rule is asked at two stages, and one
// implementation serves both. Every attachment surface an Aggregate or a Release
// declares is reachable while those documents are in view and is validated there;
// an attachment authored inside a sub-aggregate module is not, because that
// module's document is loaded only as the graph is flattened. Both stages call this
// function, so neither can hold a different idea of what a valid socket name is.
//
// Both halts survive the relaxation on the component's own `uses` surface, and
// their survival is deliberate. An attachment does not author the application's
// topology; it participates in the topology the attached Feature declares, so the
// Feature's usesSchema is the whole of what its sockets may be. This function
// therefore is not a stale twin of [ValidateUsesAgainstSchema] left un-relaxed by
// accident — the two enforce a same-shaped condition on surfaces with different
// authority, and separating them is what lets one relax without the other.
func ValidateFeatureUsesAgainstSchema(entity, fieldPath string, uses types.OrderedMap[types.UsesSocketValue], featType string, catalog CatalogLookup) []*ResolverError {
	catFeat, found := catalog.Feature(featType)
	if !found {
		return nil
	}

	if catFeat.Spec.UsesSchema.Len() == 0 {
		var errs []*ResolverError
		for socket := range uses.All() {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s[%q]", fieldPath, socket),
				Message: fmt.Sprintf("feature type %q has no usesSchema — uses declarations are not allowed", featType),
				Err:     ErrUnknownUsesSocket(),
			})
		}
		return errs
	}

	var errs []*ResolverError
	for socket := range uses.All() {
		if _, ok := catFeat.Spec.UsesSchema.Get(socket); !ok {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s[%q]", fieldPath, socket),
				Message: fmt.Sprintf("socket %q not defined in feature type %q usesSchema (valid: %v)", socket, featType, catFeat.Spec.UsesSchema.Keys()),
				Err:     ErrUnknownUsesSocket(),
			})
		}
	}
	return errs
}
