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
	synthesized  bool
	entry        types.ReleaseUsesEntry
}

// releaseComponentFieldPath renders the authoring field locator for a Release
// boundary component, the Release-side mirror of [componentFieldPath]. A
// synthesized-default component renders at the root shape
// `.spec.components["node"]` — where the author typed it — while a component in
// a declared boundary renders the qualified `.spec.boundaries["path"].components["node"]`.
// The key is the synthesized-provenance flag, not the path string: a boundary a
// user declares with the name "default" still renders the qualified path.
func releaseComponentFieldPath(boundaryPath, nodeID string, synthesized bool) string {
	if synthesized {
		return fmt.Sprintf(".spec.components[%q]", nodeID)
	}
	return fmt.Sprintf(".spec.boundaries[%q].components[%q]", boundaryPath, nodeID)
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
		for nodeID, bc := range b.Components.All() {
			for socket, aliases := range bc.Uses.All() {
				for alias, e := range aliases.All() {
					out = append(out, releaseEdgeEntry{
						nodeID:       nodeID,
						socket:       socket,
						alias:        alias,
						boundaryPath: path,
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

// guardReleaseUsesSocket is the Release-side mirror of guardUsesSocket: it
// validates that every socket a Release per-edge `uses` entry names exists in
// the consuming component's catalog usesSchema, firing ErrUnknownUsesSocket per
// undeclared socket. The check shifts left to Step 1 — where the resolver owns
// the socket-allowlist machinery and the catalog — so a Release wiring an edge
// into a non-existent socket halts before topology draws it.
//
// The consuming node's component type is resolved from the Aggregate's
// resolver-visible instances (top-level + subsystem). A Release edge on a node
// whose type the resolver cannot see — a sub-aggregate component, which loads
// only at topology — skips here; that coverage boundary is shared with the
// per-edge conformance gate.
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
				releaseComponentFieldPath(ref.boundaryPath, ref.nodeID, ref.synthesized), compType); verr != nil {
				errs = append(errs, verr)
			}
			continue
		}
		if _, ok := catComp.Spec.UsesSchema.Get(ref.socket); !ok {
			errs = append(errs, &ResolverError{
				Entity: entity,
				Field:  fmt.Sprintf("%s.uses[%q]", releaseComponentFieldPath(ref.boundaryPath, ref.nodeID, ref.synthesized), ref.socket),
				Message: fmt.Sprintf("socket %q not defined in component type %q usesSchema (valid: %v)",
					ref.socket, compType, catComp.Spec.UsesSchema.Keys()),
				Err: ErrUnknownUsesSocket,
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
	var walk func(path string, sub types.Subsystem)
	walk = func(path string, sub types.Subsystem) {
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
		for childName, child := range sub.Subsystems.All() {
			walk(path+"."+childName, child)
		}
	}
	for name, sub := range agg.Spec.Subsystems.All() {
		walk(name, sub)
	}
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
	var walk func(path string, b types.Boundary)
	walk = func(path string, b types.Boundary) {
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
		for nodeID, bc := range b.Components.All() {
			for name, feat := range bc.Features.All() {
				if feat.Uses.Len() == 0 {
					continue
				}
				out = append(out, scopeAttachment{
					field: fmt.Sprintf("%s.features[%q].uses",
						releaseComponentFieldPath(path, nodeID, b.Synthesized), name),
					featType: feat.Type,
					uses:     feat.Uses,
				})
			}
		}
		for childName, child := range b.Boundaries.All() {
			childPath := childName
			if path != "" {
				childPath = path + "/" + childName
			}
			walk(childPath, child)
		}
	}
	for name, b := range release.Spec.Boundaries.All() {
		walk(name, b)
	}
	return out
}

// guardUsesSocket validates that every socket name in uses blocks exists as a key
// in the source catalog entity's usesSchema. Components without usesSchema cannot
// accept uses declarations. This guarantees that every edge drawn in Step 2 has a
// corresponding usesSchema entry with a selector for Step 4 validation.
//
// Every surface that can author a `uses` block is walked, not only the component
// instance's own: an attachment declared at a subsystem, at a Release boundary, or
// on a Release boundary component names sockets on the same Feature entity and is
// validated against the same usesSchema. Where the attachment is declared changes
// which components its edge is contributed from; it does not change what a valid
// socket name is, so a single rule covers all four surfaces and the coverage of
// the rule does not depend on the author's choice of scope.
func guardUsesSocket(na types.NormalizedAggregate, release *types.Release, catalog CatalogLookup) []*ResolverError {
	agg := na.Aggregate()
	if agg == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	if catalog.Feature != nil {
		for _, sa := range collectAggregateScopeAttachments(na) {
			errs = append(errs, validateFeatureUsesAgainstSchema(
				entity, sa.field, sa.uses, sa.featType, catalog)...)
		}
		if release != nil {
			relEntity := fmt.Sprintf("Release %s", release.Metadata.Name)
			for _, sa := range collectReleaseScopeAttachments(release) {
				errs = append(errs, validateFeatureUsesAgainstSchema(
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
					errs = append(errs, validateFeatureUsesAgainstSchema(
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

// ValidateUsesAgainstSchema validates a component instance's uses block
// against the catalog Component's usesSchema. Two failure modes:
//
//   - The catalog Component declares no usesSchema (empty). Any uses
//     declaration on its instances is rejected — the component has no
//     defined sockets, so nothing can be connected to it. Emits one
//     ErrUnknownUsesSocket per declared socket name.
//   - The catalog Component declares a non-empty usesSchema, but the
//     instance names a socket not present in the schema. Emits
//     ErrUnknownUsesSocket per offending socket, with the valid socket
//     names (in usesSchema author order) attached to the message.
//
// Silently returns nil when the component type is not in the catalog —
// unknown-type reporting belongs to other guards.
func ValidateUsesAgainstSchema(entity, fieldPath string, uses types.OrderedMap[types.UsesSocketValue], compType string, catalog CatalogLookup) []*ResolverError {
	catComp, found := catalog.Component(compType)
	if !found {
		// A resolved bare-token catalog ref absent here is a loud view
		// inconsistency; a Tier-1 relative-path type legitimately has no entity.
		if verr := guardEntityViewInconsistency(entity, fieldPath, compType); verr != nil {
			return []*ResolverError{verr}
		}
		return nil
	}

	if catComp.Spec.UsesSchema.Len() == 0 {
		// Component has no usesSchema — uses is not allowed
		var errs []*ResolverError
		for socket := range uses.All() {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s[%q]", fieldPath, socket),
				Message: fmt.Sprintf("component type %q has no usesSchema — uses declarations are not allowed", compType),
				Err:     ErrUnknownUsesSocket,
			})
		}
		return errs
	}

	// Validate each socket name exists in usesSchema
	var errs []*ResolverError
	for socket := range uses.All() {
		if _, ok := catComp.Spec.UsesSchema.Get(socket); !ok {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s[%q]", fieldPath, socket),
				Message: fmt.Sprintf("socket %q not defined in component type %q usesSchema (valid: %v)", socket, compType, catComp.Spec.UsesSchema.Keys()),
				Err:     ErrUnknownUsesSocket,
			})
		}
	}
	return errs
}

// validateFeatureUsesAgainstSchema is the Feature-side mirror of
// ValidateUsesAgainstSchema. Same two failure modes (no usesSchema =
// declarations rejected; non-empty usesSchema = each socket must exist
// as a key) and same silent-skip on unknown catalog Feature type. Used for
// every attachment on the one `features` surface, whatever the attachment
// produces — they all resolve through the Feature catalog kind.
func validateFeatureUsesAgainstSchema(entity, fieldPath string, uses types.OrderedMap[types.UsesSocketValue], featType string, catalog CatalogLookup) []*ResolverError {
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
				Err:     ErrUnknownUsesSocket,
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
				Err:     ErrUnknownUsesSocket,
			})
		}
	}
	return errs
}
