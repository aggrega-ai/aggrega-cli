// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

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

// collectReleaseEdgeEntries walks the Release boundary tree (outer to inner,
// author order) and flattens every per-edge `uses` entry on every boundary
// component into a deterministic slice. Shared by the Step-1 guards that need
// the Release edge surface — undeclared-socket validation and per-edge infra
// conformance — so both agree on which entries exist and in what order.
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

// guardUsesSocket validates that every socket name in uses blocks exists as a key
// in the source catalog entity's usesSchema. Components without usesSchema cannot
// accept uses declarations. This guarantees that every edge drawn in Step 2 has a
// corresponding usesSchema entry with a selector for Step 4 validation.
func guardUsesSocket(na types.NormalizedAggregate, catalog CatalogLookup) []*ResolverError {
	agg := na.Aggregate()
	if agg == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	// One complete walk (root + all subsystems) via the shared instance
	// walker; the authoring path comes from componentFieldPath so a subsystem
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

			for secName, sec := range comp.Secrets.All() {
				if sec.Uses.Len() > 0 {
					errs = append(errs, validateFeatureUsesAgainstSchema(
						entity,
						fmt.Sprintf("%s.secrets[%q].uses", base, secName),
						sec.Uses,
						sec.Type,
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
// as a key) and same silent-skip on unknown catalog Feature type. Used
// for both feature attachments (.features) and secret attachments
// (.secrets), which share the Feature catalog kind.
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
