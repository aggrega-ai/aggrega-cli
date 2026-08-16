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
// component into a deterministic slice. Shared by the resolution guards that need
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

// ValidateFeatureUsesAgainstSchema validates an attachment's `uses` block against
// the attached catalog Feature's usesSchema. Two failure modes — the Feature
// declares no usesSchema, so no socket may be named; or it declares one and the
// attachment names a socket absent from it — and a silent skip on an unknown
// catalog Feature type. Used for every attachment on the one `features` surface,
// whatever the attachment produces: they all resolve through the Feature catalog
// kind.
//
// It is exported because it serves the stage the pre-flatten rules cannot reach:
// an attachment authored inside a sub-aggregate module, whose document is loaded
// only as the graph is flattened. Every attachment surface an Aggregate or a
// Release declares while those documents are still in view is measured by the
// rules declared for those surfaces instead, so the coverage boundary between the
// two is a division of labour rather than a hole.
//
// Both halts survive the relaxation on the component's own `uses` surface, and
// their survival is deliberate. An attachment does not author the application's
// topology; it participates in the topology the attached Feature declares, so the
// Feature's usesSchema is the whole of what its sockets may be. An author naming
// a socket their own component's type declares no entry for is authoring the
// product accepts, and the asymmetry between the two surfaces is what lets one
// relax without the other.
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
